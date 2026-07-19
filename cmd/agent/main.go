package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lkhmm520/portloom/internal/agent"
	"github.com/lkhmm520/portloom/internal/managedssh"
	"github.com/lkhmm520/portloom/internal/sshctl"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("portloom agent %s (commit %s, built %s)", version, commit, buildDate)
	if err := run(ctx, os.Getenv); err != nil {
		log.Printf("agent stopped: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv agent.EnvLookup) error {
	cfg, err := agent.LoadConfig(getenv)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	runner, err := sshctl.NewOpenSSHRunner(cfg.SSH)
	if err != nil {
		return fmt.Errorf("create SSH runner: %w", err)
	}
	httpClient := &http.Client{Timeout: cfg.RequestTimeout}
	credentials, err := agent.ResolveCredentials(ctx, cfg, httpClient)
	if err != nil {
		return fmt.Errorf("resolve agent credentials: %w", err)
	}
	client, err := agent.NewHTTPServerClient(cfg.ServerURL, credentials.ClientID, credentials.Token, cfg.AllowInsecureHTTP, httpClient)
	if err != nil {
		return fmt.Errorf("create server client: %w", err)
	}
	if err := agent.RegisterManagedSSHKeyWithConfig(ctx, client, agent.ManagedSSHRegistrationConfig{
		PublicKeyPath: cfg.SSHPublicKeyFile, ReadyPath: cfg.ManagedSSHReadyPath, ReadyValue: cfg.ManagedSSHReadyNonce,
		VerifyTransport: runner.EnsureMaster, DeferReady: true,
	}); err != nil {
		return fmt.Errorf("configure managed SSH access: %w", err)
	}
	reconcilerOptions := []agent.ReconcilerOption{}
	if cfg.ManagedSSHIsolated {
		bindAddress, err := managedssh.BindAddress(credentials.ClientID)
		if err != nil {
			return fmt.Errorf("derive managed SSH bind address: %w", err)
		}
		reconcilerOptions = append(reconcilerOptions, agent.WithRemoteBindHost(bindAddress))
	}
	reconciler := agent.NewReconciler(runner, agent.TCPHealthChecker{Timeout: cfg.HealthTimeout}, reconcilerOptions...)
	syncer := agent.NewSyncer(client, reconciler)
	if err := syncer.SyncOnce(ctx); err != nil {
		return fmt.Errorf("complete initial synchronization: %w", err)
	}
	if err := agent.MarkManagedSSHReady(cfg.ManagedSSHReadyPath, cfg.ManagedSSHReadyNonce); err != nil {
		return fmt.Errorf("mark Agent ready: %w", err)
	}
	err = syncer.Run(ctx, cfg.PollInterval, func(syncErr error) { log.Printf("agent synchronization failed: %v", syncErr) })
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if closeErr := runner.Close(closeCtx); closeErr != nil {
		log.Printf("SSH ControlMaster close failed: %v", closeErr)
	}
	return err
}
