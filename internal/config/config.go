package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// Config hoards every env var so nothing surprises us.
type Config struct {
	ListenAddr string
	BaseURL    *url.URL
	BasePath   string
	GotoURL    *url.URL
	LogLevel   string

	SessionKey []byte

	OIDC struct {
		Issuer       string
		ClientID     string
		ClientSecret string
		RedirectPath string
	}

	Navidrome struct {
		BaseURL   *url.URL
		AdminUser string
		AdminPass string
		TLSVerify bool
		Timeout   time.Duration
	}
}

const (
	prefix          = "ND_CP_"
	defaultListen   = ":8386"
	defaultRedirect = "/oidc/callback"
	defaultTimeout  = 10 * time.Second
	defaultLogLevel = "info"
)

// Load slurps env vars into Config before panic mode.
func Load() (*Config, error) {
	cfg := &Config{}

	cfg.ListenAddr = getEnvDefault(prefix+"LISTEN", defaultListen)

	baseURLStr := strings.TrimSpace(os.Getenv(prefix + "BASE_URL"))
	if baseURLStr == "" {
		return nil, errors.New("ND_CP_BASE_URL is required")
	}
	baseURL, err := url.Parse(baseURLStr)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("Invalid ND_CP_BASE_URL: %w", err)
	}
	cfg.BasePath = normalizeBasePath(baseURL.Path)
	baseURL.Path = ""
	baseURL.RawPath = ""
	cfg.BaseURL = baseURL

	gotoStr := strings.TrimSpace(os.Getenv(prefix + "GOTO"))
	if gotoStr == "" {
		return nil, errors.New("ND_CP_GOTO is required")
	}
	gotoURL, err := url.Parse(gotoStr)
	if err != nil || gotoURL.Scheme == "" || gotoURL.Host == "" {
		return nil, fmt.Errorf("Invalid ND_CP_GOTO: %w", err)
	}
	cfg.GotoURL = gotoURL

	cfg.LogLevel = strings.ToLower(getEnvDefault(prefix+"LOG_LEVEL", defaultLogLevel))

	sessionKey := os.Getenv(prefix + "SESSION_KEY")
	if len(sessionKey) < 32 {
		return nil, errors.New("ND_CP_SESSION_KEY must be at least 32 bytes")
	}
	cfg.SessionKey = []byte(sessionKey)

	cfg.OIDC.Issuer = strings.TrimSpace(os.Getenv(prefix + "OIDC_ISSUER"))
	if cfg.OIDC.Issuer == "" {
		return nil, errors.New("ND_CP_OIDC_ISSUER is required")
	}
	cfg.OIDC.ClientID = strings.TrimSpace(os.Getenv(prefix + "OIDC_CLIENT_ID"))
	if cfg.OIDC.ClientID == "" {
		return nil, errors.New("ND_CP_OIDC_CLIENT_ID is required")
	}
	cfg.OIDC.ClientSecret = os.Getenv(prefix + "OIDC_CLIENT_SECRET")
	cfg.OIDC.RedirectPath = getEnvDefault(prefix+"OIDC_REDIRECT_PATH", defaultRedirect)
	if !strings.HasPrefix(cfg.OIDC.RedirectPath, "/") {
		return nil, errors.New("ND_CP_OIDC_REDIRECT_PATH must start with /")
	}

	ndBase := strings.TrimSpace(os.Getenv(prefix + "ND_BASE_URL"))
	if ndBase == "" {
		return nil, errors.New("ND_CP_ND_BASE_URL is required")
	}
	ndURL, err := url.Parse(ndBase)
	if err != nil || ndURL.Scheme == "" || ndURL.Host == "" {
		return nil, fmt.Errorf("Invalid ND_CP_ND_BASE_URL: %w", err)
	}
	cfg.Navidrome.BaseURL = ndURL

	cfg.Navidrome.AdminUser = os.Getenv(prefix + "ADMIN_USER")
	if cfg.Navidrome.AdminUser == "" {
		return nil, errors.New("ND_CP_ADMIN_USER is required")
	}
	cfg.Navidrome.AdminPass = os.Getenv(prefix + "ADMIN_PASS")
	if cfg.Navidrome.AdminPass == "" {
		return nil, errors.New("ND_CP_ADMIN_PASS is required")
	}

	tlsVerifyStr := strings.TrimSpace(os.Getenv(prefix + "TLS_VERIFY"))
	if tlsVerifyStr == "" {
		cfg.Navidrome.TLSVerify = true
	} else {
		parsed, err := strconv.ParseBool(tlsVerifyStr)
		if err != nil {
			return nil, fmt.Errorf("Invalid ND_CP_TLS_VERIFY: %w", err)
		}
		cfg.Navidrome.TLSVerify = parsed
	}

	timeoutStr := strings.TrimSpace(os.Getenv(prefix + "TIMEOUT"))
	if timeoutStr == "" {
		cfg.Navidrome.Timeout = defaultTimeout
	} else {
		dur, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("Invalid ND_CP_TIMEOUT: %w", err)
		}
		cfg.Navidrome.Timeout = dur
	}

	return cfg, nil
}

// FullPath returns the absolute path for the given relative segment.
func (cfg *Config) FullPath(rel string) string {
	return JoinBasePath(cfg.BasePath, rel)
}

// AbsoluteURL builds a new URL using the configured base origin plus the joined path.
func (cfg *Config) AbsoluteURL(rel string) *url.URL {
	u := *cfg.BaseURL
	u.Path = cfg.FullPath(rel)
	return &u
}

// JoinBasePath glues relative paths onto the configured base prefix.
func JoinBasePath(basePath, rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		if basePath == "" {
			return "/"
		}
		return basePath + "/"
	}
	if basePath == "" {
		return "/" + rel
	}
	return basePath + "/" + rel
}

// getEnvDefault grabs env or shrugs to the default.
func getEnvDefault(key, def string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return def
}

func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	clean := path.Clean(p)
	if clean == "/" {
		return ""
	}
	return strings.TrimSuffix(clean, "/")
}
