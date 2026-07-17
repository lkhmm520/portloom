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
	"github.com/lkhmm520/portloom/internal/authorizedkeys"
	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/gateway"
	"github.com/lkhmm520/portloom/internal/store"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type config struct {
	ListenAddr           string
	GatewayAddr          string
	DatabasePath         string
	WebDir               string
	AdminToken           string
	AuthorizedKeysPath   string
	SSHHostPublicKeyPath string
	ManagedSSHPort       int
	ManagedSSHIsolated   bool
	PublicHost           string
	TLSAskToken          string
	TLSAskAddr           string
	PortRangeStart       int
	PortRangeEnd         int
	EnrollmentTTL        time.Duration
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
		DatabasePath: "/data/portloom.db", WebDir: "/app/web", ManagedSSHPort: 2222, TLSAskAddr: "127.0.0.1:8082",
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
	setString("TM_AUTHORIZED_KEYS_PATH", &cfg.AuthorizedKeysPath)
	setString("TM_SSH_HOST_PUBLIC_KEY_PATH", &cfg.SSHHostPublicKeyPath)
	setString("TM_PUBLIC_HOST", &cfg.PublicHost)
	setString("TM_TLS_ASK_TOKEN", &cfg.TLSAskToken)
	setString("TM_TLS_ASK_ADDR", &cfg.TLSAskAddr)
	cfg.AdminToken = strings.TrimSpace(getenv("TM_ADMIN_TOKEN"))
	var err error
	if cfg.ManagedSSHPort, err = envInt(getenv, "TM_MANAGED_SSH_PORT", cfg.ManagedSSHPort); err != nil {
		return config{}, err
	}
	if cfg.ManagedSSHIsolated, err = envBool(getenv, "TM_MANAGED_SSH_ISOLATED"); err != nil {
		return config{}, err
	}
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
	managedPaths := cfg.AuthorizedKeysPath != "" || cfg.SSHHostPublicKeyPath != ""
	if managedPaths && (cfg.AuthorizedKeysPath == "" || cfg.SSHHostPublicKeyPath == "") {
		return config{}, errors.New("managed SSH paths must be configured together")
	}
	if managedPaths && (!filepath.IsAbs(cfg.AuthorizedKeysPath) || !filepath.IsAbs(cfg.SSHHostPublicKeyPath)) {
		return config{}, errors.New("managed SSH paths must be absolute")
	}
	if cfg.ManagedSSHIsolated && !managedPaths {
		return config{}, errors.New("managed SSH isolation requires managed SSH paths")
	}
	if (cfg.PublicHost == "") != (cfg.TLSAskToken == "") {
		return config{}, errors.New("TM_PUBLIC_HOST and TM_TLS_ASK_TOKEN must be configured together")
	}
	if cfg.ManagedSSHPort < 1 || cfg.ManagedSSHPort > 65535 {
		return config{}, errors.New("invalid managed SSH port")
	}
	if cfg.PortRangeStart < 1024 || cfg.PortRangeEnd > 65535 || cfg.PortRangeStart > cfg.PortRangeEnd {
		return config{}, errors.New("invalid tunnel port range")
	}
	for _, address := range []string{cfg.ListenAddr, cfg.GatewayAddr, cfg.TLSAskAddr} {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return config{}, fmt.Errorf("invalid listen address %q: %w", address, err)
		}
	}
	if cfg.TLSAskToken != "" {
		host, _, _ := net.SplitHostPort(cfg.TLSAskAddr)
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return config{}, errors.New("TM_TLS_ASK_ADDR must use a loopback IP")
		}
	}
	return cfg, nil
}

func envBool(getenv envLookup, key string) (bool, error) {
	switch strings.TrimSpace(getenv(key)) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be true or false", key)
	}
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
	if cfg.AuthorizedKeysPath != "" {
		if err := syncAuthorizedKeys(ctx, state, cfg.AuthorizedKeysPath, cfg.ManagedSSHIsolated); err != nil {
			return fmt.Errorf("rebuild managed SSH authorization: %w", err)
		}
	}

	apiConfig := api.Config{
		AdminToken: cfg.AdminToken, EnrollmentTTL: cfg.EnrollmentTTL, AuthorizedKeysPath: cfg.AuthorizedKeysPath,
		SSHHostPublicKeyPath: cfg.SSHHostPublicKeyPath, ManagedSSHPort: cfg.ManagedSSHPort,
		ManagedSSHIsolated: cfg.ManagedSSHIsolated, ServerVersion: version,
		PublicHost: cfg.PublicHost, TLSAskToken: cfg.TLSAskToken,
	}
	control := newControlServer(cfg.ListenAddr, newMainHandler(api.New(state, apiConfig), cfg.WebDir))
	gatewayOptions := []gateway.Option{}
	if cfg.ManagedSSHIsolated {
		gatewayOptions = append(gatewayOptions, gateway.WithIsolatedAgentBindings())
	}
	data := &http.Server{Addr: cfg.GatewayAddr, Handler: gateway.New(state, gatewayOptions...), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	servers := []*http.Server{control, data}
	serverNames := []string{"control", "gateway"}
	if cfg.TLSAskToken != "" {
		tlsAsk := newControlServer(cfg.TLSAskAddr, api.NewTLSAskHandler(state, apiConfig))
		servers = append(servers, tlsAsk)
		serverNames = append(serverNames, "TLS ask")
	}
	errorsChannel := make(chan error, len(servers))
	serve := func(name string, server *http.Server) {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- fmt.Errorf("%s listener: %w", name, err)
		}
	}
	for index, server := range servers {
		go serve(serverNames[index], server)
	}
	log.Printf("control listening on %s; gateway listening on %s", cfg.ListenAddr, cfg.GatewayAddr)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errorsChannel:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = err
		}
	}
	return runErr
}

func syncAuthorizedKeys(ctx context.Context, state *store.Store, path string, isolated ...bool) error {
	return state.SyncAgentSSHKeys(ctx, func(keys []domain.AgentSSHKey) error {
		entries := make([]authorizedkeys.Entry, 0, len(keys))
		for _, key := range keys {
			entries = append(entries, authorizedkeys.Entry{AgentID: key.AgentID, PublicKey: key.PublicKey})
		}
		options := []authorizedkeys.WriteOption{}
		if len(isolated) > 0 && isolated[0] {
			options = append(options, authorizedkeys.WithIsolatedBindings())
		}
		return authorizedkeys.Write(path, entries, options...)
	})
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
