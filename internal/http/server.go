package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/azhinu/navidrome-oidc/internal/config"
	"github.com/azhinu/navidrome-oidc/internal/ctxutil"
	"github.com/azhinu/navidrome-oidc/internal/navidrome"
	"github.com/azhinu/navidrome-oidc/internal/oidc"
	"github.com/azhinu/navidrome-oidc/web"
)

// Server glues routes, sessions, and sarcasm together.
type Server struct {
	router       chi.Router
	logger       *logrus.Logger
	oidc         *oidc.Manager
	nav          *navidrome.Client
	gotoURL      string
	baseURL      *url.URL
	baseOrigin   string
	redirectPath string
	indexPage    []byte
	staticFS     http.Handler
	basePath     string
}

// New assembles the mux and prays embed files exist.
func New(cfg *config.Config, logger *logrus.Logger, oidcMgr *oidc.Manager, ndClient *navidrome.Client) (*Server, error) {
	s := &Server{
		router:       chi.NewRouter(),
		logger:       logger,
		oidc:         oidcMgr,
		nav:          ndClient,
		gotoURL:      cfg.GotoURL.String(),
		baseURL:      cfg.BaseURL,
		baseOrigin:   fmt.Sprintf("%s://%s", cfg.BaseURL.Scheme, cfg.BaseURL.Host),
		redirectPath: cfg.OIDC.RedirectPath,
		basePath:     cfg.BasePath,
	}

	sub, err := fs.Sub(web.Assets, ".")
	if err != nil {
		return nil, err
	}
	s.indexPage, err = fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}
	s.staticFS = http.FileServer(http.FS(sub))

	s.router.Use(s.requestIDMiddleware)
	s.router.Use(middleware.Recoverer)
	s.router.Use(s.loggingMiddleware)
	s.router.Use(s.bodyLimitMiddleware)

	s.routes()
	return s, nil
}

func (s *Server) routes() {
	if s.basePath != "" {
		s.router.HandleFunc(s.basePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, s.basePath+"/", http.StatusFound)
		})
	}

	s.router.Get(s.route("/"), s.handleIndex)
	s.router.Get(s.route("/login"), s.handleLogin)
	s.router.Get(s.route(s.redirectPath), s.handleCallback)
	s.router.Get(s.route("/logout"), s.handleLogout)
	s.router.Get(s.route("/api/me"), s.handleAPI(s.handleMe))
	s.router.Post(s.route("/api/password"), s.handleAPI(s.handlePassword))
	s.router.Get(s.route("/healthz"), s.handleHealth)
	s.router.Get(s.route("/readyz"), s.handleReady)

	staticPrefix := s.route("/static/")
	s.router.Handle(staticPrefix+"*", http.StripPrefix(staticPrefix, s.staticFS))
}

// ServeHTTP wraps the mux with logging and other guilt.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if _, err := s.oidc.GetSession(r); err != nil {
		http.Redirect(w, r, s.url("/login"), http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.indexPage)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if _, err := s.oidc.GetSession(r); err == nil {
		http.Redirect(w, r, s.url("/"), http.StatusFound)
		return
	}
	redirectURL, err := s.oidc.StartAuth(w, r)
	if err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "oidc_error", "Cannot start login flow")
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if _, err := s.oidc.CompleteAuth(r.Context(), w, r); err != nil {
		s.logger.WithError(err).Error("OIDC callback failed")
		s.respondError(w, r, http.StatusBadRequest, "oidc_callback_failed", "OIDC callback failed")
		return
	}
	http.Redirect(w, r, s.url("/"), http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.oidc.ClearSession(w, r)
	http.Redirect(w, r, s.url("/"), http.StatusFound)
}

func (s *Server) handleAPI(next func(http.ResponseWriter, *http.Request, *oidc.SessionData)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.oidc.GetSession(r)
		if err != nil {
			s.respondError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
			return
		}
		next(w, r, sess)
	}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, sess *oidc.SessionData) {
	ctx := r.Context()
	user, err := s.nav.GetUserByEmail(ctx, sess.Email)
	if err != nil {
		s.handleNavError(w, r, err)
		return
	}

	resp := map[string]any{
		"email":   sess.Email,
		"name":    sess.Name,
		"picture": sess.Picture,
		"exists":  user != nil,
		"nextUrl": s.gotoURL,
	}
	s.respondJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request, sess *oidc.SessionData) {
	if err := s.validateOrigin(r); err != nil {
		s.respondError(w, r, http.StatusForbidden, "csrf", "Origin mismatch")
		return
	}

	var payload struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.respondError(w, r, http.StatusBadRequest, "bad_json", "Invalid JSON body")
		return
	}
	if err := validatePassword(payload.Password); err != nil {
		s.respondError(w, r, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}

	ctx := r.Context()
	user, err := s.nav.GetUserByEmail(ctx, sess.Email)
	if err != nil {
		s.handleNavError(w, r, err)
		return
	}

	action := "created"
	if user != nil {
		if err := s.nav.UpdateUserPassword(ctx, user, payload.Password); err != nil {
			s.handleNavError(w, r, err)
			return
		}
		action = "updated"
	} else {
		name := sess.Name
		if name == "" {
			name = sess.Email
		}
		if err := s.nav.CreateUser(ctx, sess.Email, name, payload.Password); err != nil {
			s.handleNavError(w, r, err)
			return
		}
	}

	resp := map[string]any{
		"ok":      true,
		"nextUrl": s.gotoURL,
		"action":  action,
	}
	s.respondJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.nav.Ready(ctx); err != nil {
		s.respondError(w, r, http.StatusServiceUnavailable, "navidrome_unready", "Navidrome not reachable")
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) respondError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	s.respondJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func (s *Server) handleNavError(w http.ResponseWriter, r *http.Request, err error) {
	var ndErr *navidrome.Error
	if errors.As(err, &ndErr) {
		switch ndErr.Kind {
		case navidrome.ErrorAccessDenied:
			s.respondError(w, r, http.StatusBadGateway, "navidrome_access", "Проблема с доступом. Обратитесь к администратору.")
		case navidrome.ErrorUnavailable:
			s.respondError(w, r, http.StatusServiceUnavailable, "navidrome_unavailable", "Что-то пошло не так. Попробуйте позже.")
		default:
			s.respondError(w, r, http.StatusBadGateway, "navidrome_failed", "Не удалось выполнить операцию. Обратитесь к администратору.")
		}
		return
	}
	s.respondError(w, r, http.StatusInternalServerError, "unknown", "Internal error")
}

func (s *Server) validateOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return errors.New("Missing origin")
	}
	u, err := url.Parse(origin)
	if err != nil {
		return err
	}
	candidate := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	if !strings.EqualFold(candidate, s.baseOrigin) {
		return fmt.Errorf("Origin mismatch: %s", candidate)
	}
	return nil
}

func validatePassword(pw string) error {
	length := len(pw)
	if length < 6 || length > 256 {
		return errors.New("Password must be between 6 and 256 characters")
	}

	var hasUpper, hasLower, hasDigit bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit {
		return errors.New("Password must include upper-case, lower-case, and numeric characters")
	}
	return nil
}

func (s *Server) route(path string) string {
	return config.JoinBasePath(s.basePath, path)
}

func (s *Server) url(path string) string {
	return s.route(path)
}

func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		ctx := ctxutil.WithRequestID(r.Context(), reqID)
		w.Header().Set("X-Request-ID", reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		latency := time.Since(start)
		fields := logrus.Fields{
			"request_id": ctxutil.RequestID(r.Context()),
			"method":     r.Method,
			"path":       r.URL.Path,
			"status":     rw.status,
			"latency":    latency,
		}
		s.logger.WithFields(fields).Debug("HTTP request")
	})
}

func (s *Server) bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			limited := http.MaxBytesReader(w, r.Body, 4<<10)
			defer limited.Close()
			r.Body = limited
		}
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}
