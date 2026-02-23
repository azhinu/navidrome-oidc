package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/azhinu/navidrome-oidc/internal/config"
	serverpkg "github.com/azhinu/navidrome-oidc/internal/http"
	"github.com/azhinu/navidrome-oidc/internal/navidrome"
	"github.com/azhinu/navidrome-oidc/internal/oidc"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	logger.SetLevel(parseLevel(cfg.LogLevel))

	ctx := context.Background()
	redirectURL := cfg.AbsoluteURL(cfg.OIDC.RedirectPath)
	oidcMgr, err := oidc.NewManager(ctx, oidc.Config{
		Issuer:       cfg.OIDC.Issuer,
		ClientID:     cfg.OIDC.ClientID,
		ClientSecret: cfg.OIDC.ClientSecret,
		RedirectURL:  redirectURL.String(),
		SessionKey:   cfg.SessionKey,
		BaseURL:      cfg.BaseURL,
		BasePath:     cfg.BasePath,
	}, logger)
	if err != nil {
		logger.WithError(err).Error("Failed to initialise OIDC")
		os.Exit(1)
	}

	httpClient := navidrome.HTTPClient(cfg.Navidrome.Timeout, !cfg.Navidrome.TLSVerify)
	navClient := navidrome.NewClient(cfg.Navidrome.BaseURL, cfg.Navidrome.AdminUser, cfg.Navidrome.AdminPass, httpClient, logger)

	server, err := serverpkg.New(cfg, logger, oidcMgr, navClient)
	if err != nil {
		logger.WithError(err).Error("Failed to build HTTP server")
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      server,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.WithField("addr", cfg.ListenAddr).Info("Listening")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.WithError(err).Error("HTTP server stopped")
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.WithError(err).Error("Graceful shutdown failed")
		os.Exit(1)
	}
	logger.Info("Server stopped")
}

func parseLevel(level string) logrus.Level {
	switch level {
	case "debug":
		return logrus.DebugLevel
	case "warn":
		return logrus.WarnLevel
	case "error":
		return logrus.ErrorLevel
	default:
		return logrus.InfoLevel
	}
}
