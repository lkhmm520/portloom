package agent

import (
	"testing"
	"time"
)

func TestLoadConfigReadsAndValidatesEnvironment(t *testing.T) {
	env := map[string]string{"TM_SERVER_URL": "https://manager.example.com/", "TM_CLIENT_ID": "nas-01", "TM_AGENT_TOKEN": "secret-token", "TM_POLL_INTERVAL": "15s", "TM_HEALTH_TIMEOUT": "2s", "TM_REQUEST_TIMEOUT": "9s", "TM_SSH_USER": "tunnel-agent", "TM_SSH_HOST": "gateway.example.com", "TM_SSH_PORT": "2222", "TM_SSH_IDENTITY_FILE": "/run/secrets/agent_key", "TM_SSH_KNOWN_HOSTS_FILE": "/etc/portloom/known_hosts", "TM_SSH_CONTROL_PATH": "/tmp/portloom-%C.sock"}
	cfg, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ServerURL != "https://manager.example.com" || cfg.ClientID != "nas-01" {
		t.Fatalf("cfg=%#v", cfg)
	}
	if cfg.PollInterval != 15*time.Second || cfg.HealthTimeout != 2*time.Second || cfg.RequestTimeout != 9*time.Second {
		t.Fatalf("durations=%s,%s,%s", cfg.PollInterval, cfg.HealthTimeout, cfg.RequestTimeout)
	}
	if cfg.SSH.Port != 2222 || cfg.SSH.Host != "gateway.example.com" {
		t.Fatalf("ssh=%#v", cfg.SSH)
	}
}
func TestLoadConfigSupportsDeploymentEnvironmentAliases(t *testing.T) {
	env := map[string]string{"TM_SERVER_URL": "https://manager.example.com", "TM_CLIENT_NAME": "nas-home", "TM_ENROLLMENT_TOKEN": "enroll-token", "TM_HEARTBEAT_INTERVAL": "11s", "TM_SSH_USER": "tunnel", "TM_SSH_HOST": "gateway.example.com", "TM_SSH_PRIVATE_KEY_PATH": "/key", "TM_SSH_KNOWN_HOSTS_PATH": "/known_hosts"}
	cfg, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientName != "nas-home" || cfg.EnrollmentToken != "enroll-token" || cfg.ClientID != "" || cfg.Token != "" || cfg.PollInterval != 11*time.Second {
		t.Fatalf("cfg=%#v", cfg)
	}
	if cfg.SSH.IdentityFile != "/key" || cfg.SSH.KnownHostsFile != "/known_hosts" || cfg.SSH.ControlPath != "/tmp/portloom-%C.sock" {
		t.Fatalf("ssh=%#v", cfg.SSH)
	}
}

func TestLoadConfigRejectsMissingSecretsAndNonHTTPURL(t *testing.T) {
	base := map[string]string{"TM_SERVER_URL": "https://manager.example.com", "TM_CLIENT_ID": "nas-01", "TM_AGENT_TOKEN": "token", "TM_SSH_USER": "agent", "TM_SSH_HOST": "gateway.example.com", "TM_SSH_IDENTITY_FILE": "/key", "TM_SSH_KNOWN_HOSTS_FILE": "/known_hosts", "TM_SSH_CONTROL_PATH": "/tmp/control.sock"}
	mutations := []func(map[string]string){func(v map[string]string) { v["TM_AGENT_TOKEN"] = "" }, func(v map[string]string) { v["TM_SERVER_URL"] = "file:///etc/passwd" }, func(v map[string]string) { v["TM_SERVER_URL"] = "https://user:pass@example.com" }}
	for _, mutate := range mutations {
		values := map[string]string{}
		for k, v := range base {
			values[k] = v
		}
		mutate(values)
		if _, err := LoadConfig(func(key string) string { return values[key] }); err == nil {
			t.Fatalf("accepted: %#v", values)
		}
	}
}

func TestLoadConfigRequiresHTTPSUnlessExplicitlyAllowedForLoopback(t *testing.T) {
	base := map[string]string{"TM_CLIENT_ID": "nas-01", "TM_AGENT_TOKEN": "token", "TM_SSH_USER": "agent", "TM_SSH_HOST": "gateway.example.com", "TM_SSH_IDENTITY_FILE": "/key", "TM_SSH_KNOWN_HOSTS_FILE": "/known_hosts", "TM_SSH_CONTROL_PATH": "/tmp/control.sock"}
	tests := []struct {
		name, serverURL, allow string
		wantErr                bool
	}{
		{name: "https remote", serverURL: "https://manager.example.com"},
		{name: "http remote", serverURL: "http://manager.example.com", allow: "true", wantErr: true},
		{name: "http localhost defaults secure", serverURL: "http://localhost:8080", wantErr: true},
		{name: "http localhost explicitly allowed", serverURL: "http://localhost:8080", allow: "true"},
		{name: "http IPv4 loopback explicitly allowed", serverURL: "http://127.42.0.9:8080", allow: "true"},
		{name: "http IPv6 loopback explicitly allowed", serverURL: "http://[::1]:8080", allow: "true"},
		{name: "invalid opt in", serverURL: "https://manager.example.com", allow: "1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+2)
			for key, value := range base {
				env[key] = value
			}
			env["TM_SERVER_URL"] = tt.serverURL
			env["TM_ALLOW_INSECURE_HTTP"] = tt.allow
			cfg, err := LoadConfig(func(key string) string { return env[key] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg.AllowInsecureHTTP != (tt.allow == "true") {
				t.Fatalf("AllowInsecureHTTP = %v", cfg.AllowInsecureHTTP)
			}
		})
	}
}

func TestLoadConfigReadsOptionalManagedSSHPublicKeyPath(t *testing.T) {
	env := map[string]string{
		"TM_SERVER_URL": "https://manager.example.com", "TM_CLIENT_ID": "nas-01", "TM_AGENT_TOKEN": "token",
		"TM_SSH_USER": "tunnel", "TM_SSH_HOST": "gateway.example.com", "TM_SSH_IDENTITY_FILE": "/data/ssh/id_ed25519",
		"TM_SSH_PUBLIC_KEY_FILE": "/data/ssh/id_ed25519.pub", "TM_SSH_KNOWN_HOSTS_FILE": "/data/ssh/known_hosts",
		"TM_MANAGED_SSH_ISOLATED": "true", "TM_MANAGED_SSH_READY_PATH": "/run/portloom/managed-ssh.ready", "TM_MANAGED_SSH_READY_NONCE": "generation-123",
	}
	cfg, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSHPublicKeyFile != "/data/ssh/id_ed25519.pub" {
		t.Fatalf("public key path = %q", cfg.SSHPublicKeyFile)
	}
	if !cfg.ManagedSSHIsolated || cfg.ManagedSSHReadyPath != "/run/portloom/managed-ssh.ready" || cfg.ManagedSSHReadyNonce != "generation-123" {
		t.Fatalf("managed SSH config=%#v", cfg)
	}
}

func TestLoadConfigRejectsUnsafeManagedSSHReadyNonce(t *testing.T) {
	env := map[string]string{
		"TM_SERVER_URL": "https://manager.example.com", "TM_CLIENT_ID": "nas-01", "TM_AGENT_TOKEN": "token",
		"TM_SSH_USER": "tunnel", "TM_SSH_HOST": "gateway.example.com", "TM_SSH_IDENTITY_FILE": "/data/ssh/id_ed25519",
		"TM_SSH_PUBLIC_KEY_FILE": "/data/ssh/id_ed25519.pub", "TM_SSH_KNOWN_HOSTS_FILE": "/data/ssh/known_hosts",
		"TM_MANAGED_SSH_READY_NONCE": "bad\nnonce",
	}
	if _, err := LoadConfig(func(key string) string { return env[key] }); err == nil {
		t.Fatal("unsafe managed SSH ready nonce accepted")
	}
}
