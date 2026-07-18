package sshctl

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type execution struct {
	path string
	args []string
}
type recordingExecutor struct {
	calls []execution
	err   error
}

func (e *recordingExecutor) Run(_ context.Context, path string, args []string) error {
	e.calls = append(e.calls, execution{path: path, args: append([]string(nil), args...)})
	return e.err
}
func validSSHConfig() Config {
	return Config{User: "tunnel-agent", Host: "gateway.example.com", Port: 2222, IdentityFile: "/run/secrets/agent_key", KnownHostsFile: "/etc/portloom/known_hosts", ControlPath: "/tmp/portloom-%C.sock", ConnectTimeout: 7}
}
func newTestRunner(t *testing.T, executor Executor) *OpenSSHRunner {
	t.Helper()
	runner, err := NewOpenSSHRunner(validSSHConfig(), WithExecutor(executor))
	if err != nil {
		t.Fatalf("NewOpenSSHRunner: %v", err)
	}
	return runner
}

func TestEnsureMasterUsesFixedExecutableAndArgumentArray(t *testing.T) {
	executor := &recordingExecutor{}
	runner := newTestRunner(t, executor)
	if err := runner.EnsureMaster(context.Background()); err != nil {
		t.Fatalf("EnsureMaster: %v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("calls=%d", len(executor.calls))
	}
	call := executor.calls[0]
	if call.path != SSHExecutable {
		t.Fatalf("path=%q", call.path)
	}
	want := []string{"-M", "-N", "-f", "-o", "ControlMaster=yes", "-o", "ControlPersist=yes", "-o", "ControlPath=/tmp/portloom-%C.sock", "-o", "ExitOnForwardFailure=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/etc/portloom/known_hosts", "-o", "ConnectTimeout=7", "-i", "/run/secrets/agent_key", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if !reflect.DeepEqual(call.args, want) {
		t.Fatalf("args:\n got %#v\nwant %#v", call.args, want)
	}
}
func TestEnsureMasterUsesUnbracketedIPv6Destination(t *testing.T) {
	executor := &recordingExecutor{}
	cfg := validSSHConfig()
	cfg.Host = "2001:db8::1"
	runner, err := NewOpenSSHRunner(cfg, WithExecutor(executor))
	if err != nil {
		t.Fatalf("NewOpenSSHRunner: %v", err)
	}
	if err := runner.EnsureMaster(context.Background()); err != nil {
		t.Fatalf("EnsureMaster: %v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("calls=%d", len(executor.calls))
	}
	got := executor.calls[0].args[len(executor.calls[0].args)-1]
	if want := "tunnel-agent@2001:db8::1"; got != want {
		t.Fatalf("destination=%q want=%q", got, want)
	}
}

func TestCheckMasterUsesFixedExecutableAndArgumentArray(t *testing.T) {
	executor := &recordingExecutor{}
	runner := newTestRunner(t, executor)
	if err := runner.CheckMaster(context.Background()); err != nil {
		t.Fatalf("CheckMaster: %v", err)
	}
	want := []string{"-S", "/tmp/portloom-%C.sock", "-O", "check", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if len(executor.calls) != 1 || executor.calls[0].path != SSHExecutable || !reflect.DeepEqual(executor.calls[0].args, want) {
		t.Fatalf("calls=%#v", executor.calls)
	}
}
func TestForwardAndCancelUseOpenSSHControlCommands(t *testing.T) {
	executor := &recordingExecutor{}
	runner := newTestRunner(t, executor)
	f := Forward{BindHost: "127.0.0.1", RemotePort: 14001, LocalHost: "192.168.1.20", LocalPort: 8080}
	if err := runner.Forward(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if err := runner.Cancel(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("calls=%d", len(executor.calls))
	}
	wantForward := []string{"-S", "/tmp/portloom-%C.sock", "-O", "forward", "-R", "127.0.0.1:14001:192.168.1.20:8080", "-p", "2222", "tunnel-agent@gateway.example.com"}
	wantCancel := []string{"-S", "/tmp/portloom-%C.sock", "-O", "cancel", "-R", "127.0.0.1:14001:192.168.1.20:8080", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if !reflect.DeepEqual(executor.calls[0].args, wantForward) {
		t.Fatalf("forward=%#v", executor.calls[0].args)
	}
	if !reflect.DeepEqual(executor.calls[1].args, wantCancel) {
		t.Fatalf("cancel=%#v", executor.calls[1].args)
	}
}
func TestCloseUsesControlMasterExit(t *testing.T) {
	executor := &recordingExecutor{}
	runner := newTestRunner(t, executor)
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"-S", "/tmp/portloom-%C.sock", "-O", "exit", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if len(executor.calls) != 1 || !reflect.DeepEqual(executor.calls[0].args, want) {
		t.Fatalf("calls=%#v", executor.calls)
	}
}
func TestConfigRejectsArgumentInjectionAndUnsafeForward(t *testing.T) {
	cases := []Config{func() Config { c := validSSHConfig(); c.User = "root -oProxyCommand=bad"; return c }(), func() Config { c := validSSHConfig(); c.Host = "gateway;touch /tmp/pwned"; return c }(), func() Config { c := validSSHConfig(); c.ControlPath = "relative.sock"; return c }(), func() Config { c := validSSHConfig(); c.KnownHostsFile = ""; return c }()}
	for _, cfg := range cases {
		if _, err := NewOpenSSHRunner(cfg); err == nil {
			t.Fatalf("accepted: %#v", cfg)
		}
	}
	runner := newTestRunner(t, &recordingExecutor{})
	for _, f := range []Forward{{BindHost: "0.0.0.0", RemotePort: 1000, LocalHost: "127.0.0.1", LocalPort: 80}, {BindHost: "127.0.0.1", RemotePort: 0, LocalHost: "127.0.0.1", LocalPort: 80}, {BindHost: "127.0.0.1", RemotePort: 1000, LocalHost: "host;bad", LocalPort: 80}} {
		if err := runner.Forward(context.Background(), f); err == nil {
			t.Fatalf("accepted: %#v", f)
		}
	}
}
func TestExecutorErrorIsWrapped(t *testing.T) {
	runner := newTestRunner(t, &recordingExecutor{err: errors.New("exit status 255")})
	err := runner.Forward(context.Background(), Forward{BindHost: "127.0.0.1", RemotePort: 14001, LocalHost: "127.0.0.1", LocalPort: 8080})
	if err == nil || !strings.Contains(err.Error(), "forward") || !strings.Contains(err.Error(), "exit status 255") {
		t.Fatalf("err=%v", err)
	}
}
