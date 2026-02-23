package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

const (
	sessionName      = "ndcp_session"
	loginSessionName = "ndcp_login"
	sessionTTL       = 24 * time.Hour
	loginTTL         = 10 * time.Minute
)

var ErrNoSession = errors.New("Session not found")

// SessionData is the bare minimum we stash in cookies.
type SessionData struct {
	Email     string
	Name      string
	Picture   string
	ExpiresAt time.Time
}

// Manager babysits OIDC plus cranky cookies.
type Manager struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
	store    *sessions.CookieStore
	secure   bool
	baseURL  *url.URL
	redirect string
	basePath string
	logger   *logrus.Logger
}

// Config is the junk drawer for OIDC wiring.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	SessionKey   []byte
	BaseURL      *url.URL
	BasePath     string
}

// NewManager pokes discovery and arms the verifier.
func NewManager(ctx context.Context, cfg Config, logger *logrus.Logger) (*Manager, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		RedirectURL:  cfg.RedirectURL,
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	store := sessions.NewCookieStore(cfg.SessionKey)
	secure := strings.EqualFold(cfg.BaseURL.Scheme, "https")
	store.Options = &sessions.Options{
		Path:     cookiePath(cfg.BasePath),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	}

	if logger == nil {
		logger = logrus.StandardLogger()
	}

	return &Manager{
		provider: provider,
		verifier: verifier,
		oauth:    oauthCfg,
		store:    store,
		secure:   secure,
		baseURL:  cfg.BaseURL,
		redirect: cfg.RedirectURL,
		basePath: cfg.BasePath,
		logger:   logger,
	}, nil
}

// StartAuth bakes PKCE goodies and hands out the redirect.
func (m *Manager) StartAuth(w http.ResponseWriter, r *http.Request) (string, error) {
	state := uuid.NewString()
	nonce := uuid.NewString()
	verifier, challenge, err := pkcePair()
	if err != nil {
		return "", err
	}

	sess, err := m.store.Get(r, loginSessionName)
	if err != nil {
		return "", err
	}
	sess.Options = &sessions.Options{
		Path:     cookiePath(m.basePath),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(loginTTL.Seconds()),
	}
	sess.Values["state"] = state
	sess.Values["nonce"] = nonce
	sess.Values["code_verifier"] = verifier
	if err := sess.Save(r, w); err != nil {
		return "", err
	}

	opts := []oauth2.AuthCodeOption{
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}

	return m.oauth.AuthCodeURL(state, opts...), nil
}

// CompleteAuth trades the code for tokens and hope.
func (m *Manager) CompleteAuth(ctx context.Context, w http.ResponseWriter, r *http.Request) (*SessionData, error) {
	loginSess, err := m.store.Get(r, loginSessionName)
	if err != nil {
		return nil, err
	}
	state, _ := loginSess.Values["state"].(string)
	codeVerifier, _ := loginSess.Values["code_verifier"].(string)
	nonce, _ := loginSess.Values["nonce"].(string)
	loginSess.Options.MaxAge = -1
	_ = loginSess.Save(r, w)

	if state == "" || codeVerifier == "" || nonce == "" {
		return nil, errors.New("OIDC login session missing state")
	}

	if r.URL.Query().Get("state") != state {
		return nil, errors.New("Invalid state parameter")
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		return nil, errors.New("Missing authorization code")
	}

	token, err := m.oauth.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	if err != nil {
		return nil, fmt.Errorf("Token exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("ID token not returned")
	}

	idToken, err := m.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("ID token verify failed: %w", err)
	}

	if idToken.Nonce != nonce {
		return nil, errors.New("Nonce mismatch")
	}

	var claims struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		m.logger.WithError(err).Warn("ID token claims decode failed")
		return nil, fmt.Errorf("Decode claims: %w", err)
	}

	if claims.Email == "" {
		return nil, errors.New("Email was not provided by the identity provider, ask your administrator")
	}

	data := &SessionData{
		Email:     claims.Email,
		Name:      claims.Name,
		Picture:   claims.Picture,
		ExpiresAt: idToken.Expiry,
	}
	if data.ExpiresAt.Before(time.Now()) || data.ExpiresAt.After(time.Now().Add(sessionTTL)) {
		data.ExpiresAt = time.Now().Add(sessionTTL)
	}

	if err := m.saveSession(w, r, data); err != nil {
		return nil, err
	}

	return data, nil
}

func (m *Manager) saveSession(w http.ResponseWriter, r *http.Request, data *SessionData) error {
	sess, err := m.store.Get(r, sessionName)
	if err != nil {
		return err
	}
	sess.Values["email"] = data.Email
	sess.Values["name"] = data.Name
	sess.Values["picture"] = data.Picture
	sess.Values["expires"] = data.ExpiresAt.Unix()
	ttl := time.Until(data.ExpiresAt)
	if ttl <= 0 {
		ttl = sessionTTL
		data.ExpiresAt = time.Now().Add(ttl)
	}
	if ttl > sessionTTL {
		ttl = sessionTTL
	}
	sess.Options = &sessions.Options{
		Path:     cookiePath(m.basePath),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	}
	return sess.Save(r, w)
}

// GetSession coughs up the session or sulks.
func (m *Manager) GetSession(r *http.Request) (*SessionData, error) {
	sess, err := m.store.Get(r, sessionName)
	if err != nil {
		return nil, ErrNoSession
	}
	email, _ := sess.Values["email"].(string)
	if email == "" {
		return nil, ErrNoSession
	}
	expiresUnix, _ := sess.Values["expires"].(int64)
	if expiresUnix == 0 {
		if expFloat, ok := sess.Values["expires"].(float64); ok {
			expiresUnix = int64(expFloat)
		}
	}
	expires := time.Unix(expiresUnix, 0)
	if time.Now().After(expires) {
		return nil, ErrNoSession
	}
	return &SessionData{
		Email:     email,
		Name:      strVal(sess.Values["name"]),
		Picture:   strVal(sess.Values["picture"]),
		ExpiresAt: expires,
	}, nil
}

// ClearSession vaporizes the cookie.
func (m *Manager) ClearSession(w http.ResponseWriter, r *http.Request) error {
	sess, err := m.store.Get(r, sessionName)
	if err != nil {
		return err
	}
	sess.Options.MaxAge = -1
	return sess.Save(r, w)
}

// RedirectPath exists so callers stop guessing.
func (m *Manager) RedirectPath() string { return m.redirect }

// strVal avoids interface{} melodrama.
func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// pkcePair spits out the verifier+challenge combo.
func pkcePair() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

func cookiePath(basePath string) string {
	if basePath == "" {
		return "/"
	}
	return basePath + "/"
}
