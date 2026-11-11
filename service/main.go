// url-health-api registers URLs, checks them on an interval and exposes their
// status over HTTP plus Prometheus metrics. Configuration is env-only so the
// same binary runs locally (memory store) and on GKE (Firestore via Workload
// Identity, no keys).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// demo: version endpoint touched on 2026-11-11 04:29 UTC
// Version is set at build time with -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := newStore(ctx, cfg)
	if err != nil {
		logger.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	checker := NewChecker(store, cfg.CheckInterval, cfg.CheckTimeout)
	go checker.Run(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           NewRouter(store, checker),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("listening", "addr", srv.Addr, "version", Version, "store", cfg.Store, "interval", cfg.CheckInterval.String())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

type Config struct {
	Port          string
	Store         string // "memory" | "firestore"
	Project       string
	Collection    string
	CheckInterval time.Duration
	CheckTimeout  time.Duration
}

func loadConfig() Config {
	cfg := Config{
		Port:          env("PORT", "8080"),
		Store:         env("STORE", "memory"),
		Project:       env("GOOGLE_CLOUD_PROJECT", ""),
		Collection:    env("FIRESTORE_COLLECTION", "checks"),
		CheckInterval: envDuration("CHECK_INTERVAL", 60*time.Second),
		CheckTimeout:  envDuration("CHECK_TIMEOUT", 5*time.Second),
	}
	return cfg
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	return def
}

func newStore(ctx context.Context, cfg Config) (Store, error) {
	switch cfg.Store {
	case "firestore":
		return NewFirestoreStore(ctx, cfg.Project, cfg.Collection)
	case "memory":
		return NewMemoryStore(), nil
	default:
		return nil, errors.New("STORE must be memory or firestore")
	}
}
