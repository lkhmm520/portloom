package sshctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const SSHExecutable = "/usr/bin/ssh"

var (
	userPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,31}$`)
	hostPattern = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*)$`)
)

type Config struct {
	User           string
	Host           string
	Port           int
	IdentityFile   string
	KnownHostsFile string
	ControlPath    string
	ConnectTimeout int
}

type Forward struct {
	BindHost   string
	RemotePort int
	LocalHost  string
	LocalPort  int
}

type Executor interface {
	Run(context.Context, string, []string) error
}
type executorFunc func(context.Context, string, []string) error

func (f executorFunc) Run(ctx context.Context, path string, args []string) error {
	return f(ctx, path, args)
}

type Option func(*OpenSSHRunner)

func WithExecutor(executor Executor) Option {
	return func(r *OpenSSHRunner) {
		if executor != nil {
			r.executor = executor
		}
	}
}

type OpenSSHRunner struct {
	config   Config
	executor Executor
}

func NewOpenSSHRunner(config Config, options ...Option) (*OpenSSHRunner, error) {
	if config.Port == 0 {
		config.Port = 22
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 10
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	runner := &OpenSSHRunner{config: config, executor: executorFunc(runCommand)}
	for _, option := range options {
		option(runner)
	}
	return runner, nil
}

func validateConfig(config Config) error {
	if !userPattern.MatchString(config.User) {
		return errors.New("invalid SSH user")
	}
	if !validHost(config.Host) {
		return errors.New("invalid SSH host")
	}
	if !validPort(config.Port) {
		return errors.New("SSH port must be between 1 and 65535")
	}
	if config.ConnectTimeout < 1 || config.ConnectTimeout > 300 {
		return errors.New("SSH connect timeout must be between 1 and 300 seconds")
	}
	for name, path := range map[string]string{"identity file": config.IdentityFile, "known hosts file": config.KnownHostsFile, "control path": config.ControlPath} {
		if !safeAbsolutePath(path) {
			return fmt.Errorf("%s must be a safe absolute path", name)
		}
	}
	return nil
}
func safeAbsolutePath(path string) bool {
	return path != "" && filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\r\n") && filepath.Clean(path) == path
}
func validPort(port int) bool { return port >= 1 && port <= 65535 }
func validHost(host string) bool {
	return net.ParseIP(host) != nil || (len(host) <= 253 && hostPattern.MatchString(host))
}

func (r *OpenSSHRunner) EnsureMaster(ctx context.Context) error {
	cfg := r.config
	args := []string{"-M", "-N", "-f", "-o", "ControlMaster=yes", "-o", "ControlPersist=yes", "-o", "ControlPath=" + cfg.ControlPath, "-o", "ExitOnForwardFailure=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + cfg.KnownHostsFile, "-o", "ConnectTimeout=" + strconv.Itoa(cfg.ConnectTimeout), "-i", cfg.IdentityFile, "-p", strconv.Itoa(cfg.Port), destination(cfg)}
	if err := r.executor.Run(ctx, SSHExecutable, args); err != nil {
		return fmt.Errorf("start SSH ControlMaster: %w", err)
	}
	return nil
}
func (r *OpenSSHRunner) CheckMaster(ctx context.Context) error {
	args := []string{"-S", r.config.ControlPath, "-O", "check", "-p", strconv.Itoa(r.config.Port), destination(r.config)}
	if err := r.executor.Run(ctx, SSHExecutable, args); err != nil {
		return fmt.Errorf("check SSH ControlMaster: %w", err)
	}
	return nil
}
func (r *OpenSSHRunner) Forward(ctx context.Context, forward Forward) error {
	return r.controlForward(ctx, "forward", forward)
}
func (r *OpenSSHRunner) Cancel(ctx context.Context, forward Forward) error {
	return r.controlForward(ctx, "cancel", forward)
}
func (r *OpenSSHRunner) controlForward(ctx context.Context, operation string, forward Forward) error {
	if err := validateForward(forward); err != nil {
		return err
	}
	args := []string{"-S", r.config.ControlPath, "-O", operation, "-R", formatForward(forward), "-p", strconv.Itoa(r.config.Port), destination(r.config)}
	if err := r.executor.Run(ctx, SSHExecutable, args); err != nil {
		return fmt.Errorf("SSH %s: %w", operation, err)
	}
	return nil
}
func (r *OpenSSHRunner) Close(ctx context.Context) error {
	args := []string{"-S", r.config.ControlPath, "-O", "exit", "-p", strconv.Itoa(r.config.Port), destination(r.config)}
	if err := r.executor.Run(ctx, SSHExecutable, args); err != nil {
		return fmt.Errorf("close SSH ControlMaster: %w", err)
	}
	return nil
}
func validateForward(forward Forward) error {
	bind := net.ParseIP(forward.BindHost)
	if bind == nil || !bind.IsLoopback() {
		return errors.New("remote bind host must be a loopback IP")
	}
	if !validPort(forward.RemotePort) || !validPort(forward.LocalPort) {
		return errors.New("forward ports must be between 1 and 65535")
	}
	if !validHost(forward.LocalHost) {
		return errors.New("invalid local forward host")
	}
	return nil
}
func destination(config Config) string {
	host := config.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return config.User + "@" + host
}
func formatForward(forward Forward) string {
	return endpointHost(forward.BindHost) + ":" + strconv.Itoa(forward.RemotePort) + ":" + endpointHost(forward.LocalHost) + ":" + strconv.Itoa(forward.LocalPort)
}
func endpointHost(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func runCommand(ctx context.Context, path string, args []string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		text := strings.TrimSpace(output.String())
		if len(text) > 1024 {
			text = text[:1024]
		}
		if text != "" {
			return fmt.Errorf("%w: %s", err, text)
		}
		return err
	}
	return nil
}
