package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lkhmm520/portloom/internal/api"
	"github.com/lkhmm520/portloom/internal/gateway"
	"github.com/lkhmm520/portloom/internal/store"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type config struct {
	ListenAddr     string
	GatewayAddr    string
	DatabasePath   string
	WebDir         string
	AdminToken     string
	PortRangeStart int
	PortRangeEnd   int
	EnrollmentTTL  time.Duration
}

type envLookup func(string) string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("portloom server %s (commit %s, built %s)", version, commit, buildDate)
	if err := run(ctx, os.Getenv); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

func loadConfig(getenv envLookup) (config, error) {
	cfg := config{
		ListenAddr: "127.0.0.1:8080", GatewayAddr: "127.0.0.1:8081",
		DatabasePath: "/data/portloom.db", WebDir: "/app/web",
		PortRangeStart: 20000, PortRangeEnd: 29999, EnrollmentTTL: time.Hour,
	}
	setString := func(key string, target *string) {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			*target = value
		}
	}
	setString("TM_LISTEN_ADDR", &cfg.ListenAddr)
	setString("TM_GATEWAY_ADDR", &cfg.GatewayAddr)
	setString("TM_DATABASE_PATH", &cfg.DatabasePath)
	setString("TM_WEB_DIR", &cfg.WebDir)
	cfg.AdminToken = strings.TrimSpace(getenv("TM_ADMIN_TOKEN"))
	var err error
	if cfg.PortRangeStart, err = envInt(getenv, "TM_PORT_RANGE_START", cfg.PortRangeStart); err != nil {
		return config{}, err
	}
	if cfg.PortRangeEnd, err = envInt(getenv, "TM_PORT_RANGE_END", cfg.PortRangeEnd); err != nil {
		return config{}, err
	}
	if raw := strings.TrimSpace(getenv("TM_ENROLLMENT_TTL")); raw != "" {
		cfg.EnrollmentTTL, err = time.ParseDuration(raw)
		if err != nil || cfg.EnrollmentTTL <= 0 {
			return config{}, errors.New("TM_ENROLLMENT_TTL must be a positive duration")
		}
	}
	if len(cfg.AdminToken) < 16 {
		return config{}, errors.New("TM_ADMIN_TOKEN must contain at least 16 characters")
	}
	if !filepath.IsAbs(cfg.DatabasePath) || !filepath.IsAbs(cfg.WebDir) {
		return config{}, errors.New("database and web paths must be absolute")
	}
	if cfg.PortRangeStart < 1024 || cfg.PortRangeEnd > 65535 || cfg.PortRangeStart > cfg.PortRangeEnd {
		return config{}, errors.New("invalid tunnel port range")
	}
	for _, address := range []string{cfg.ListenAddr, cfg.GatewayAddr} {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return config{}, fmt.Errorf("invalid listen address %q: %w", address, err)
		}
	}
	return cfg, nil
}

func envInt(getenv envLookup, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func run(ctx context.Context, getenv envLookup) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	if info, err := os.Stat(cfg.WebDir); err != nil || !info.IsDir() {
		return fmt.Errorf("web directory unavailable: %s", cfg.WebDir)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	state, err := store.Open(cfg.DatabasePath, store.Options{PortRangeStart: cfg.PortRangeStart, PortRangeEnd: cfg.PortRangeEnd})
	if err != nil {
		return err
	}
	defer state.Close()

	control := newControlServer(cfg.ListenAddr, newMainHandler(api.New(state, api.Config{AdminToken: cfg.AdminToken, EnrollmentTTL: cfg.EnrollmentTTL}), cfg.WebDir))
	data := &http.Server{Addr: cfg.GatewayAddr, Handler: gateway.New(state), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	errorsChannel := make(chan error, 2)
	serve := func(name string, server *http.Server) {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- fmt.Errorf("%s listener: %w", name, err)
		}
	}
	go serve("control", control)
	go serve("gateway", data)
	log.Printf("control listening on %s; gateway listening on %s", cfg.ListenAddr, cfg.GatewayAddr)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errorsChannel:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := control.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = err
	}
	if err := data.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

func newControlServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func newMainHandler(apiHandler http.Handler, webDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/api/", apiHandler)
	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
