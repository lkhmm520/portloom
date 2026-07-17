package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type registrarFunc func(context.Context, string) error

func (f registrarFunc) RegisterSSHKey(ctx context.Context, key string) error { return f(ctx, key) }

type failingSSHKeyRegistrar struct {
	failures int
	calls    int
	called   chan struct{}
}

func (r *failingSSHKeyRegistrar) RegisterSSHKey(context.Context, string) error {
	r.calls++
	if r.called != nil {
		select {
		case r.called <- struct{}{}:
		default:
		}
	}
	if r.calls <= r.failures {
		return errors.New("temporary registration failure")
	}
	return nil
}

type recordingSSHKeyRegistrar struct {
	key   string
	calls int
}

func (r *recordingSSHKeyRegistrar) RegisterSSHKey(_ context.Context, key string) error {
	r.key = key
	r.calls++
	return nil
}

func TestRegisterManagedSSHKeyReadsConfiguredPublicKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(path, []byte("ssh-ed25519 AAAA comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registrar := &recordingSSHKeyRegistrar{}
	if err := RegisterManagedSSHKey(context.Background(), registrar, path); err != nil {
		t.Fatal(err)
	}
	if registrar.calls != 1 || registrar.key != "ssh-ed25519 AAAA comment" {
		t.Fatalf("calls=%d key=%q", registrar.calls, registrar.key)
	}
}

func TestRegisterManagedSSHKeySkipsEmptyPath(t *testing.T) {
	registrar := &recordingSSHKeyRegistrar{}
	if err := RegisterManagedSSHKey(context.Background(), registrar, ""); err != nil {
		t.Fatal(err)
	}
	if registrar.calls != 0 {
		t.Fatalf("calls=%d", registrar.calls)
	}
}

func TestRegisterManagedSSHKeyRetriesAndWritesReadyMarker(t *testing.T) {
	dir := t.TempDir()
	publicKeyPath := filepath.Join(dir, "id_ed25519.pub")
	readyPath := filepath.Join(dir, "managed-ssh.ready")
	if err := os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAA comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registrar := &failingSSHKeyRegistrar{failures: 2}
	err := RegisterManagedSSHKeyWithConfig(context.Background(), registrar, ManagedSSHRegistrationConfig{
		PublicKeyPath: publicKeyPath, ReadyPath: readyPath, ReadyValue: "generation-123", InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if registrar.calls != 3 {
		t.Fatalf("registration calls=%d, want 3", registrar.calls)
	}
	if data, err := os.ReadFile(readyPath); err != nil || string(data) != "generation-123\n" {
		t.Fatalf("ready marker data=%q err=%v", data, err)
	}
}

func TestRegisterManagedSSHKeyRetryWaitRespondsToContext(t *testing.T) {
	dir := t.TempDir()
	publicKeyPath := filepath.Join(dir, "id_ed25519.pub")
	readyPath := filepath.Join(dir, "managed-ssh.ready")
	if err := os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readyPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	registrar := &failingSSHKeyRegistrar{failures: 100, called: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() {
		done <- RegisterManagedSSHKeyWithConfig(ctx, registrar, ManagedSSHRegistrationConfig{
			PublicKeyPath: publicKeyPath, ReadyPath: readyPath, InitialBackoff: time.Hour, MaxBackoff: time.Hour,
		})
	}()
	select {
	case <-registrar.called:
	case <-time.After(time.Second):
		t.Fatal("registration was not attempted")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry did not stop after cancellation")
	}
	if _, err := os.Stat(readyPath); !os.IsNotExist(err) {
		t.Fatalf("stale ready marker remains: %v", err)
	}
}

func TestRegisterManagedSSHKeyRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 17<<10)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterManagedSSHKey(context.Background(), &recordingSSHKeyRegistrar{}, path); err == nil {
		t.Fatal("oversized key accepted")
	}
}

func TestManagedSSHReadyWaitsForTransportVerification(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id.pub")
	if err := os.WriteFile(keyPath, []byte("ssh-ed25519 AAAA test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(t.TempDir(), "managed-ssh.ready")
	attempts := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := RegisterManagedSSHKeyWithConfig(ctx, registrarFunc(func(context.Context, string) error { return nil }), ManagedSSHRegistrationConfig{
		PublicKeyPath:  keyPath,
		ReadyPath:      readyPath,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		VerifyTransport: func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("SSH unavailable")
			}
			if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("ready marker existed before transport verification: %v", err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("transport attempts=%d", attempts)
	}
	if _, err := os.Stat(readyPath); err != nil {
		t.Fatalf("ready marker missing: %v", err)
	}
}
