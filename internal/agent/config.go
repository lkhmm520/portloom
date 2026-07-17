package agent

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/sshctl"
)

type Config struct {
	ServerURL            string
	AllowInsecureHTTP    bool
	ClientName           string
	ClientID             string
	Token                string
	EnrollmentToken      string
	StatePath            string
	SSHPublicKeyFile     string
	ManagedSSHIsolated   bool
	ManagedSSHReadyPath  string
	ManagedSSHReadyNonce string
	PollInterval         time.Duration
	HealthTimeout        time.Duration
	RequestTimeout       time.Duration
	SSH                  sshctl.Config
}

type EnvLookup func(string) string

func LoadConfig(getenv EnvLookup) (Config, error) {
	cfg := Config{
		ServerURL:            strings.TrimRight(strings.TrimSpace(getenv("TM_SERVER_URL")), "/"),
		ClientName:           strings.TrimSpace(getenv("TM_CLIENT_NAME")),
		ClientID:             strings.TrimSpace(getenv("TM_CLIENT_ID")),
		Token:                strings.TrimSpace(getenv("TM_AGENT_TOKEN")),
		EnrollmentToken:      strings.TrimSpace(getenv("TM_ENROLLMENT_TOKEN")),
		StatePath:            firstNonEmpty(getenv("TM_AGENT_STATE_PATH"), "/data/agent.json"),
		SSHPublicKeyFile:     strings.TrimSpace(getenv("TM_SSH_PUBLIC_KEY_FILE")),
		ManagedSSHReadyPath:  firstNonEmpty(getenv("TM_MANAGED_SSH_READY_PATH"), "/data/managed-ssh.ready"),
		ManagedSSHReadyNonce: strings.TrimSpace(getenv("TM_MANAGED_SSH_READY_NONCE")),
		PollInterval:         30 * time.Second,
		HealthTimeout:        3 * time.Second,
		RequestTimeout:       10 * time.Second,
		SSH: sshctl.Config{
			User:           strings.TrimSpace(getenv("TM_SSH_USER")),
			Host:           strings.TrimSpace(getenv("TM_SSH_HOST")),
			Port:           22,
			IdentityFile:   firstNonEmpty(getenv("TM_SSH_IDENTITY_FILE"), getenv("TM_SSH_PRIVATE_KEY_PATH")),
			KnownHostsFile: firstNonEmpty(getenv("TM_SSH_KNOWN_HOSTS_FILE"), getenv("TM_SSH_KNOWN_HOSTS_PATH")),
			ControlPath:    firstNonEmpty(getenv("TM_SSH_CONTROL_PATH"), "/tmp/portloom-%C.sock"),
			ConnectTimeout: 10,
		},
	}
	var err error
	if cfg.AllowInsecureHTTP, err = parseExplicitBool(getenv("TM_ALLOW_INSECURE_HTTP"), "TM_ALLOW_INSECURE_HTTP"); err != nil {
		return Config{}, err
	}
	if cfg.ManagedSSHIsolated, err = parseExplicitBool(getenv("TM_MANAGED_SSH_ISOLATED"), "TM_MANAGED_SSH_ISOLATED"); err != nil {
		return Config{}, err
	}
	pollValue := firstNonEmpty(getenv("TM_POLL_INTERVAL"), getenv("TM_HEARTBEAT_INTERVAL"))
	if cfg.PollInterval, err = parseDuration(pollValue, cfg.PollInterval, "poll interval"); err != nil {
		return Config{}, err
	}
	if cfg.HealthTimeout, err = parseDuration(getenv("TM_HEALTH_TIMEOUT"), cfg.HealthTimeout, "health timeout"); err != nil {
		return Config{}, err
	}
	if cfg.RequestTimeout, err = parseDuration(getenv("TM_REQUEST_TIMEOUT"), cfg.RequestTimeout, "request timeout"); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(getenv("TM_SSH_PORT")); raw != "" {
		cfg.SSH.Port, err = strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid SSH port: %w", err)
		}
	}
	if raw := strings.TrimSpace(getenv("TM_SSH_CONNECT_TIMEOUT")); raw != "" {
		cfg.SSH.ConnectTimeout, err = strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid SSH connect timeout: %w", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func parseDuration(raw string, fallback time.Duration, name string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return value, nil
}

func parseExplicitBool(raw, name string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func (c Config) Validate() error {
	if _, err := parseServerURL(c.ServerURL, c.AllowInsecureHTTP); err != nil {
		return err
	}
	if (c.ClientID == "") != (c.Token == "") {
		return errors.New("client ID and agent token must be provided together")
	}
	if c.ClientID == "" {
		if c.ClientName == "" {
			return errors.New("client name is required before enrollment")
		}
		if c.StatePath == "" {
			return errors.New("agent state path is required")
		}
	}
	if c.ManagedSSHIsolated && strings.TrimSpace(c.SSHPublicKeyFile) == "" {
		return errors.New("managed SSH isolation requires an SSH public key file")
	}
	if c.SSHPublicKeyFile != "" && (!filepath.IsAbs(c.SSHPublicKeyFile) || !filepath.IsAbs(c.ManagedSSHReadyPath)) {
		return errors.New("managed SSH public key and ready paths must be absolute")
	}
	if len(c.ManagedSSHReadyNonce) > 128 || strings.ContainsAny(c.ManagedSSHReadyNonce, "\r\n\x00") {
		return errors.New("managed SSH ready nonce is invalid")
	}
	if c.PollInterval <= 0 || c.HealthTimeout <= 0 || c.RequestTimeout <= 0 {
		return errors.New("agent durations must be positive")
	}
	if _, err := sshctl.NewOpenSSHRunner(c.SSH); err != nil {
		return fmt.Errorf("invalid SSH configuration: %w", err)
	}
	return nil
}
