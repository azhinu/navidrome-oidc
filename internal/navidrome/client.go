package navidrome

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ErrorKind tags which part of Navidrome had a bad day.
type ErrorKind string

const (
	ErrorAccessDenied ErrorKind = "access_denied"
	ErrorUnavailable  ErrorKind = "unavailable"
	ErrorOperation    ErrorKind = "operation_failed"
)

// Error wraps the mess so callers can panic politely.
type Error struct {
	Kind   ErrorKind
	Status int
	Err    error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("Navidrome: %v", e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Client pesters Navidrome's admin API without remorse.
type Client struct {
	baseURL   *url.URL
	adminUser string
	adminPass string
	http      *http.Client
	logger    *logrus.Logger

	mu    sync.RWMutex
	token string
}

// NewClient wires the basics; BYO patience.
func NewClient(base *url.URL, adminUser, adminPass string, httpClient *http.Client, logger *logrus.Logger) *Client {
	return &Client{
		baseURL:   base,
		adminUser: adminUser,
		adminPass: adminPass,
		http:      httpClient,
		logger:    logger,
	}
}

// User is the tiny slice of payload we tolerate.
type User struct {
	ID              string `json:"id"`
	UserName        string `json:"userName"`
	Name            string `json:"name"`
	Email           string `json:"email"`
	IsAdmin         bool   `json:"isAdmin"`
	IsLocked        bool   `json:"isLocked"`
	IsEnabled       bool   `json:"isEnabled"`
	InvitationEmail string `json:"invitationEmail"`
	ChangePassword  bool   `json:"changePassword"`
	raw             map[string]any
}

func (u *User) UnmarshalJSON(data []byte) error {
	type Alias User
	aux := (*Alias)(u)
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if u.raw == nil {
		u.raw = make(map[string]any)
	}
	if err := json.Unmarshal(data, &u.raw); err != nil {
		return err
	}
	return nil
}

func (u *User) payloadWithPassword(password string) map[string]any {
	payload := make(map[string]any, len(u.raw)+2)
	for k, v := range u.raw {
		payload[k] = v
	}
	payload["userName"] = u.UserName
	payload["name"] = u.Name
	payload["email"] = u.Email
	payload["isAdmin"] = u.IsAdmin
	payload["isLocked"] = u.IsLocked
	payload["isEnabled"] = u.IsEnabled
	if u.InvitationEmail != "" {
		payload["invitationEmail"] = u.InvitationEmail
	}
	payload["password"] = password
	payload["changePassword"] = true
	return payload
}

// Ready pokes Navidrome to see if it's awake.
func (c *Client) Ready(ctx context.Context) error {
	if err := c.ensureToken(ctx, true); err != nil {
		return err
	}
	// Light ping to ensure Navidrome still breathes.
	_, err := c.GetUserByEmail(ctx, "__nonexistent__")
	if err != nil {
		var ndErr *Error
		if errors.As(err, &ndErr) && ndErr.Kind == ErrorAccessDenied {
			return err
		}
	}
	return nil
}

// GetUserByEmail hunts down a user via brute-force filtering.
func (c *Client) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	query := url.Values{}
	filterMap := map[string]string{"userName": email}
	filterBytes, _ := json.Marshal(filterMap)
	query.Set("filter", string(filterBytes))
	var users []User
	rel := &url.URL{Path: "/api/user", RawQuery: query.Encode()}
	if err := c.do(ctx, http.MethodGet, rel, nil, &users); err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].UserName == email {
			return &users[i], nil
		}
	}
	return nil, nil
}

// CreateUser births yet another Navidrome account.
func (c *Client) CreateUser(ctx context.Context, email, name, password string) error {
	payload := map[string]any{
		"userName": email,
		"name":     name,
		"email":    email,
		"password": password,
		"isAdmin":  false,
	}
	rel := &url.URL{Path: "/api/user"}
	return c.do(ctx, http.MethodPost, rel, payload, nil)
}

// UpdateUserPassword bullies Navidrome into accepting a new secret.
func (c *Client) UpdateUserPassword(ctx context.Context, user *User, password string) error {
	path := fmt.Sprintf("/api/user/%s", user.ID)
	payload := user.payloadWithPassword(password)
	rel := &url.URL{Path: path}
	return c.do(ctx, http.MethodPut, rel, payload, nil)
}

func (c *Client) do(ctx context.Context, method string, rel *url.URL, body any, out any) error {
	if err := c.ensureToken(ctx, false); err != nil {
		return err
	}

	reqURL := c.baseURL.ResolveReference(rel)
	var bodyBytes []byte
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
		bodyBytes = buf.Bytes()
	}
	bodyPreview := sanitizeBody(bodyBytes)

	retried := false
	for {
		var reader io.Reader
		if bodyBytes != nil {
			reader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), reader)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")

		token := c.getToken()
		if token != "" {
			req.Header.Set("X-ND-Authorization", "Bearer "+token)
		}

		start := time.Now()
		resp, err := c.http.Do(req)
		latency := time.Since(start)
		if err != nil {
			c.logRequest(ctx, method, rel, 0, latency, bodyPreview, err)
			return &Error{Kind: ErrorUnavailable, Err: err}
		}

		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			if retried {
				c.clearToken()
				err := &Error{Kind: ErrorAccessDenied, Status: resp.StatusCode, Err: fmt.Errorf("Admin credentials rejected")}
				c.logRequest(ctx, method, rel, resp.StatusCode, latency, bodyPreview, err)
				return err
			}
			retried = true
			if err := c.ensureToken(ctx, true); err != nil {
				return err
			}
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			err := &Error{Kind: ErrorUnavailable, Status: resp.StatusCode, Err: fmt.Errorf("Navidrome server error %d", resp.StatusCode)}
			c.logRequest(ctx, method, rel, resp.StatusCode, latency, bodyPreview, err)
			return err
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			err := &Error{Kind: ErrorOperation, Status: resp.StatusCode, Err: fmt.Errorf("Navidrome rejected request (%d)", resp.StatusCode)}
			c.logRequest(ctx, method, rel, resp.StatusCode, latency, bodyPreview, err)
			return err
		}

		if out != nil {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				c.logRequest(ctx, method, rel, resp.StatusCode, latency, bodyPreview, err)
				return err
			}
		} else {
			resp.Body.Close()
		}
		c.logRequest(ctx, method, rel, resp.StatusCode, latency, bodyPreview, nil)
		return nil
	}
}

func (c *Client) ensureToken(ctx context.Context, force bool) error {
	c.mu.RLock()
	tkn := c.token
	c.mu.RUnlock()
	if tkn != "" && !force {
		return nil
	}

	return c.login(ctx)
}

func (c *Client) login(ctx context.Context) error {
	payload := map[string]string{
		"username": c.adminUser,
		"password": c.adminPass,
	}

	rel := &url.URL{Path: "/auth/login"}
	reqURL := c.baseURL.ResolveReference(rel)
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return err
	}
	bodyBytes := buf.Bytes()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.http.Do(req)
	latency := time.Since(start)
	if err != nil {
		c.logRequest(ctx, http.MethodPost, rel, 0, latency, "", err)
		return &Error{Kind: ErrorUnavailable, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		err := &Error{Kind: ErrorAccessDenied, Status: resp.StatusCode, Err: errors.New("Invalid admin credentials")}
		c.logRequest(ctx, http.MethodPost, rel, resp.StatusCode, latency, "", err)
		return err
	}
	if resp.StatusCode >= 400 {
		err := &Error{Kind: ErrorUnavailable, Status: resp.StatusCode, Err: fmt.Errorf("Login failed with %d", resp.StatusCode)}
		c.logRequest(ctx, http.MethodPost, rel, resp.StatusCode, latency, "", err)
		return err
	}

	var res struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		c.logRequest(ctx, http.MethodPost, rel, resp.StatusCode, latency, "", err)
		return err
	}
	if res.Token == "" {
		err := &Error{Kind: ErrorUnavailable, Err: errors.New("Login response missing token")}
		c.logRequest(ctx, http.MethodPost, rel, resp.StatusCode, latency, "", err)
		return err
	}

	c.mu.Lock()
	c.token = res.Token
	c.mu.Unlock()
	c.logRequest(ctx, http.MethodPost, rel, resp.StatusCode, latency, "", nil)
	return nil
}

func (c *Client) getToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

func (c *Client) clearToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// HTTPClient hands back a tuned http.Client, caffeine included.
func HTTPClient(timeout time.Duration, skipVerify bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if skipVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true // #nosec G402
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func (c *Client) logRequest(ctx context.Context, method string, rel *url.URL, status int, latency time.Duration, body string, ndErr error) {
	if c.logger == nil {
		return
	}
	fields := logrus.Fields{
		"method": method,
	}
	if rel != nil {
		fields["endpoint"] = rel.Path
		if rel.RawQuery != "" {
			fields["query"] = rel.RawQuery
		}
	}
	if body != "" {
		fields["body"] = body
	}
	if status != 0 {
		fields["status"] = status
	}
	entry := c.logger.WithFields(fields)
	if ndErr != nil {
		entry.WithField("nd_error", ndErr.Error()).Error("Navidrome request failed")
		return
	}
	entry.Info("Navidrome request")
}

func sanitizeBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return truncateBody(string(raw))
	}
	sanitizeValue(data)
	buf, err := json.Marshal(data)
	if err != nil {
		return truncateBody(string(raw))
	}
	return truncateBody(string(buf))
}

func sanitizeValue(v any) {
	switch val := v.(type) {
	case map[string]any:
		for k := range val {
			if strings.Contains(strings.ToLower(k), "password") {
				val[k] = "***REDACTED***"
				continue
			}
			sanitizeValue(val[k])
		}
	case []any:
		for i := range val {
			sanitizeValue(val[i])
		}
	}
}

func truncateBody(body string) string {
	const maxBody = 2048
	if len(body) <= maxBody {
		return body
	}
	return body[:maxBody] + "...(truncated)"
}
