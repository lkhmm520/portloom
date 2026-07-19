package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxSSHPublicKeyBytes       int64 = 16 << 10
	defaultRegistrationBackoff       = 250 * time.Millisecond
	maxRegistrationBackoff           = 30 * time.Second
)

type SSHKeyRegistrar interface {
	RegisterSSHKey(context.Context, string) error
}

type ManagedSSHRegistrationConfig struct {
	PublicKeyPath   string
	ReadyPath       string
	ReadyValue      string
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	VerifyTransport func(context.Context) error
	DeferReady      bool
}

func RegisterManagedSSHKey(ctx context.Context, registrar SSHKeyRegistrar, path string) error {
	return RegisterManagedSSHKeyWithConfig(ctx, registrar, ManagedSSHRegistrationConfig{PublicKeyPath: path})
}

func RegisterManagedSSHKeyWithConfig(ctx context.Context, registrar SSHKeyRegistrar, config ManagedSSHRegistrationConfig) error {
	config.PublicKeyPath = strings.TrimSpace(config.PublicKeyPath)
	config.ReadyPath = strings.TrimSpace(config.ReadyPath)
	config.ReadyValue = strings.TrimSpace(config.ReadyValue)
	if config.ReadyValue == "" {
		config.ReadyValue = "ready"
	}
	if strings.ContainsAny(config.ReadyValue, "\r\n\x00") {
		return errors.New("managed SSH ready value is invalid")
	}
	if config.PublicKeyPath == "" {
		return nil
	}
	if registrar == nil {
		return errors.New("SSH key registrar is required")
	}
	if config.InitialBackoff <= 0 {
		config.InitialBackoff = defaultRegistrationBackoff
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = maxRegistrationBackoff
	}
	if config.MaxBackoff < config.InitialBackoff {
		config.MaxBackoff = config.InitialBackoff
	}
	if config.ReadyPath != "" {
		if err := os.Remove(config.ReadyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale managed SSH ready marker: %w", err)
		}
	}

	key, err := readSSHPublicKey(config.PublicKeyPath)
	if err != nil {
		return err
	}
	backoff := config.InitialBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := registrar.RegisterSSHKey(ctx, key)
		if err == nil && config.VerifyTransport != nil {
			err = config.VerifyTransport(ctx)
			if err != nil {
				err = fmt.Errorf("verify managed SSH transport: %w", err)
			}
		}
		if err == nil {
			if config.ReadyPath != "" && !config.DeferReady {
				if err := MarkManagedSSHReady(config.ReadyPath, config.ReadyValue); err != nil {
					return err
				}
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if !temporaryRegistrationError(err) {
			return fmt.Errorf("register SSH public key: %w", err)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < config.MaxBackoff {
			backoff *= 2
			if backoff > config.MaxBackoff {
				backoff = config.MaxBackoff
			}
		}
	}
}

func readSSHPublicKey(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open SSH public key: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSSHPublicKeyBytes+1))
	if err != nil {
		return "", fmt.Errorf("read SSH public key: %w", err)
	}
	if int64(len(data)) > maxSSHPublicKeyBytes {
		return "", errors.New("SSH public key file exceeds size limit")
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("SSH public key file is empty")
	}
	return key, nil
}

func temporaryRegistrationError(err error) bool {
	var temporary interface{ Temporary() bool }
	return !errors.As(err, &temporary) || temporary.Temporary()
}

func MarkManagedSSHReady(path, value string) error {
	path = strings.TrimSpace(path)
	value = strings.TrimSpace(value)
	if path == "" {
		return nil
	}
	if value == "" {
		value = "ready"
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("managed SSH ready value is invalid")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create managed SSH ready directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".managed-ssh-ready-*")
	if err != nil {
		return fmt.Errorf("create managed SSH ready marker: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect managed SSH ready marker: %w", err)
	}
	if _, err := file.WriteString(value + "\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("write managed SSH ready marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync managed SSH ready marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close managed SSH ready marker: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install managed SSH ready marker: %w", err)
	}
	return nil
}
