package sshctl

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type execution struct {
	path string
	args []string
}
type recordingExecutor struct {
	calls []execution
	err   error
	errs  []error
}

func (e *recordingExecutor) Run(_ context.Context, path string, args []string) error {
	e.calls = append(e.calls, execution{path: path, args: append([]string(nil), args...)})
	if len(e.errs) > 0 {
		err := e.errs[0]
		e.errs = e.errs[1:]
		return err
	}
	return e.err
}
func validSSHConfig(t *testing.T) Config {
	t.Helper()
	return Config{User: "tunnel-agent", Host: "gateway.example.com", Port: 2222, IdentityFile: "/run/secrets/agent_key", KnownHostsFile: "/etc/portloom/known_hosts", ControlPath: filepath.Join(privateTempDir(t), "%C.sock"), ConnectTimeout: 7}
}
func privateTempDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func withoutControlPathLockForTest() Option {
	return func(r *OpenSSHRunner) { r.disableControlLockForTest = true }
}
func newTestRunner(t *testing.T, executor Executor) *OpenSSHRunner {
	t.Helper()
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest())
	if err != nil {
		t.Fatalf("NewOpenSSHRunner: %v", err)
	}
	return runner
}

func TestEnsureMasterUsesFixedExecutableAndArgumentArray(t *testing.T) {
	executor := &recordingExecutor{errs: []error{errControlMasterAbsent}}
	runner := newTestRunner(t, executor)
	if err := runner.EnsureMaster(context.Background()); err != nil {
		t.Fatalf("EnsureMaster: %v", err)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("calls=%d", len(executor.calls))
	}
	call := executor.calls[1]
	if call.path != SSHExecutable {
		t.Fatalf("path=%q", call.path)
	}
	want := []string{"-F", "/dev/null", "-M", "-N", "-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-o", "ControlPath=" + runner.config.ControlPath, "-o", "ExitOnForwardFailure=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/etc/portloom/known_hosts", "-o", "ConnectTimeout=7", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-i", "/run/secrets/agent_key", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if !reflect.DeepEqual(call.args, want) {
		t.Fatalf("args:\n got %#v\nwant %#v", call.args, want)
	}
}
func TestEnsureMasterReplacesUnmanagedExistingMaster(t *testing.T) {
	executor := &recordingExecutor{errs: []error{nil, nil, errControlMasterAbsent, nil, nil}}
	runner := newTestRunner(t, executor)
	if err := runner.EnsureMaster(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(executor.calls) != 5 {
		t.Fatalf("calls=%#v", executor.calls)
	}
	if got := executor.calls[0].args; len(got) < 6 || got[4] != "-O" || got[5] != "check" {
		t.Fatalf("first call is not check: %#v", got)
	}
	if got := executor.calls[1].args; len(got) < 6 || got[4] != "-O" || got[5] != "exit" {
		t.Fatalf("second call is not exit: %#v", got)
	}
	if got := executor.calls[2].args; len(got) < 6 || got[4] != "-O" || got[5] != "check" {
		t.Fatalf("third call does not confirm exit: %#v", got)
	}
	if got := executor.calls[3].args; len(got) < 3 || got[2] != "-M" {
		t.Fatalf("fourth call is not master start: %#v", got)
	}
	if got := executor.calls[4].args; len(got) < 6 || got[4] != "-O" || got[5] != "check" {
		t.Fatalf("fifth call is not readiness check: %#v", got)
	}
}

func TestEnsureMasterKeepsHealthyOwnedMaster(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "owned-master-*")
	if err != nil {
		t.Fatal(err)
	}
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: make(chan struct{})}
	executor := &recordingExecutor{}
	runner := newTestRunner(t, executor)
	runner.master = process
	t.Cleanup(func() {
		close(process.done)
		process.cleanup()
	})
	if err := runner.EnsureMaster(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(executor.calls) != 1 || len(executor.calls[0].args) < 4 || executor.calls[0].args[5] != "check" {
		t.Fatalf("healthy owned master was replaced: %#v", executor.calls)
	}
}

func TestEnsureMasterAndCloseSerializeLifecycleTransitions(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "serialized-master-*")
	if err != nil {
		t.Fatal(err)
	}
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: make(chan struct{})}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	executor := executorFunc(func(context.Context, string, []string) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	})
	runner := newTestRunner(t, executor)
	runner.master = process
	ensureDone := make(chan error, 1)
	closeDone := make(chan error, 1)
	go func() { ensureDone <- runner.EnsureMaster(context.Background()) }()
	<-started
	go func() { closeDone <- runner.Close(context.Background()) }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close overlapped EnsureMaster: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("executor calls overlapped lifecycle transition: %d", got)
	}
	close(release)
	if err := <-ensureDone; err != nil {
		t.Fatal(err)
	}
	close(process.done)
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(output.Name()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("diagnostic file survived serialized close: %v", statErr)
	}
}

func TestEnsureMasterRequiresReadyControlSocket(t *testing.T) {
	executor := &recordingExecutor{errs: []error{
		errControlMasterAbsent,
		nil,
		errors.New("control socket is not ready"),
		nil,
		errControlMasterAbsent,
	}}
	runner := newTestRunner(t, executor)
	err := runner.EnsureMaster(context.Background())
	if err == nil || !strings.Contains(err.Error(), "verify SSH ControlMaster readiness") {
		t.Fatalf("err=%v", err)
	}
	if len(executor.calls) != 5 || len(executor.calls[3].args) < 4 || executor.calls[3].args[5] != "exit" || executor.calls[4].args[5] != "check" {
		t.Fatalf("readiness failure cleanup was not confirmed: %#v", executor.calls)
	}
}

func TestEnsureMasterHasIndependentStartupTimeout(t *testing.T) {
	calls := 0
	executor := executorFunc(func(ctx context.Context, _ string, _ []string) error {
		calls++
		if calls == 1 {
			return errControlMasterAbsent
		}
		<-ctx.Done()
		return ctx.Err()
	})
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest(), WithMasterStartupTimeout(25*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = runner.EnsureMaster(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("startup timeout was not enforced: %s", elapsed)
	}
}

func TestEnsureMasterUsesUnbracketedIPv6Destination(t *testing.T) {
	executor := &recordingExecutor{errs: []error{errControlMasterAbsent}}
	cfg := validSSHConfig(t)
	cfg.Host = "2001:db8::1"
	runner, err := NewOpenSSHRunner(cfg, WithExecutor(executor), withoutControlPathLockForTest())
	if err != nil {
		t.Fatalf("NewOpenSSHRunner: %v", err)
	}
	if err := runner.EnsureMaster(context.Background()); err != nil {
		t.Fatalf("EnsureMaster: %v", err)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("calls=%d", len(executor.calls))
	}
	got := executor.calls[1].args[len(executor.calls[1].args)-1]
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
	want := []string{"-F", "/dev/null", "-S", runner.config.ControlPath, "-O", "check", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if len(executor.calls) != 1 || executor.calls[0].path != SSHExecutable || !reflect.DeepEqual(executor.calls[0].args, want) {
		t.Fatalf("calls=%#v", executor.calls)
	}
}

func TestControlMasterAbsenceClassificationIsNarrow(t *testing.T) {
	for _, err := range []error{
		errControlMasterAbsent,
		errors.New("Control socket connect(/tmp/master.sock): No such file or directory"),
	} {
		if !isControlMasterAbsent(err) {
			t.Fatalf("definitive absence not recognized: %v", err)
		}
	}
	for _, err := range []error{
		errors.New("Control socket connect(/tmp/master.sock): Connection refused"),
		errors.New("control socket connect(/tmp/master.sock): connection refused\n"),
	} {
		if !isControlMasterStale(err) {
			t.Fatalf("stale socket not recognized: %v", err)
		}
	}
	for _, err := range []error{
		context.DeadlineExceeded,
		errors.New("Control socket connect(/tmp/master.sock): Permission denied"),
		errors.New("Control socket connect(/tmp/no such file or directory): Permission denied"),
		errors.New("process exited: exit status 255"),
	} {
		if isControlMasterAbsent(err) {
			t.Fatalf("unknown state classified as absent: %v", err)
		}
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
	wantForward := []string{"-F", "/dev/null", "-S", runner.config.ControlPath, "-O", "forward", "-R", "127.0.0.1:14001:192.168.1.20:8080", "-p", "2222", "tunnel-agent@gateway.example.com"}
	wantCancel := []string{"-F", "/dev/null", "-S", runner.config.ControlPath, "-O", "cancel", "-R", "127.0.0.1:14001:192.168.1.20:8080", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if !reflect.DeepEqual(executor.calls[0].args, wantForward) {
		t.Fatalf("forward=%#v", executor.calls[0].args)
	}
	if !reflect.DeepEqual(executor.calls[1].args, wantCancel) {
		t.Fatalf("cancel=%#v", executor.calls[1].args)
	}
}
func TestCloseControlUsesControlMasterExit(t *testing.T) {
	executor := &recordingExecutor{}
	runner := newTestRunner(t, executor)
	if err := runner.closeControl(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"-F", "/dev/null", "-S", runner.config.ControlPath, "-O", "exit", "-p", "2222", "tunnel-agent@gateway.example.com"}
	if len(executor.calls) != 1 || !reflect.DeepEqual(executor.calls[0].args, want) {
		t.Fatalf("calls=%#v", executor.calls)
	}
}

func TestCloseStopsUnmanagedExistingMaster(t *testing.T) {
	executor := &recordingExecutor{errs: []error{nil, nil, errControlMasterAbsent}}
	runner := newTestRunner(t, executor)
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(executor.calls) != 3 || executor.calls[0].args[5] != "check" || executor.calls[1].args[5] != "exit" || executor.calls[2].args[5] != "check" {
		t.Fatalf("unmanaged master exit was not confirmed: %#v", executor.calls)
	}
}

func TestCloseFailsWhenUnmanagedMasterStateIsUnknown(t *testing.T) {
	executor := &recordingExecutor{err: context.DeadlineExceeded}
	runner := newTestRunner(t, executor)
	err := runner.Close(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err=%v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("unexpected cleanup after unknown state: %#v", executor.calls)
	}
}

func TestEnsureMasterDoesNotReplaceWhenExitConfirmationIsUnknown(t *testing.T) {
	executor := &recordingExecutor{errs: []error{nil, nil, context.DeadlineExceeded}}
	runner := newTestRunner(t, executor)
	err := runner.EnsureMaster(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnsureMaster err=%v", err)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("replacement started without confirmed exit: %#v", executor.calls)
	}
}

func TestCloseFailsWhenUnmanagedExitConfirmationIsUnknown(t *testing.T) {
	executor := &recordingExecutor{errs: []error{nil, nil, context.DeadlineExceeded}}
	runner := newTestRunner(t, executor)
	err := runner.Close(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err=%v", err)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("Close accepted unconfirmed exit: %#v", executor.calls)
	}
}

func TestEnsureMasterCallerCancellationDoesNotKillOwnedMaster(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "owned-master-*")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	done := make(chan struct{})
	var kills atomic.Int32
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: done, killFn: func() error {
		kills.Add(1)
		close(done)
		return nil
	}}
	executor := executorFunc(func(ctx context.Context, _ string, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest(), WithOperationTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	runner.master = process
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = runner.EnsureMaster(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnsureMaster err=%v", err)
	}
	if kills.Load() != 0 || runner.currentMaster() != process {
		t.Fatalf("caller cancellation killed or released owned master: kills=%d master=%p", kills.Load(), runner.currentMaster())
	}
}

func TestEnsureMasterCallerCancellationDuringGracefulReplacementDoesNotKillOwnedMaster(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "owned-master-grace-*")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	done := make(chan struct{})
	var kills atomic.Int32
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: done, killFn: func() error {
		kills.Add(1)
		close(done)
		return nil
	}}
	var calls atomic.Int32
	executor := executorFunc(func(ctx context.Context, _ string, _ []string) error {
		if calls.Add(1) == 1 {
			return errors.New("owned master health check failed")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest(), WithOperationTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	runner.master = process
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = runner.EnsureMaster(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnsureMaster err=%v", err)
	}
	if kills.Load() != 0 || runner.currentMaster() != process {
		t.Fatalf("caller cancellation during replacement killed or released master: kills=%d master=%p", kills.Load(), runner.currentMaster())
	}
}

func TestLifecycleAdmissionHonorsCallerDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	executor := executorFunc(func(context.Context, string, []string) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	})
	runner := newTestRunner(t, executor)
	forwardDone := make(chan error, 1)
	go func() {
		forwardDone <- runner.Forward(context.Background(), Forward{BindHost: "127.0.0.1", RemotePort: 14001, LocalHost: "127.0.0.1", LocalPort: 8080})
	}()
	<-started
	time.AfterFunc(200*time.Millisecond, func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	begin := time.Now()
	err := runner.CheckMaster(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CheckMaster err=%v", err)
	}
	if elapsed := time.Since(begin); elapsed > 100*time.Millisecond {
		t.Fatalf("lifecycle admission ignored caller deadline: %s", elapsed)
	}
	if err := <-forwardDone; err != nil {
		t.Fatal(err)
	}
}
func TestControlOperationsHaveIndependentTimeouts(t *testing.T) {
	executor := executorFunc(func(ctx context.Context, _ string, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest(), WithOperationTimeout(25*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	forward := Forward{BindHost: "127.0.0.1", RemotePort: 14001, LocalHost: "127.0.0.1", LocalPort: 8080}
	operations := []struct {
		name string
		run  func() error
	}{
		{name: "check", run: func() error { return runner.CheckMaster(context.Background()) }},
		{name: "forward", run: func() error { return runner.Forward(context.Background(), forward) }},
		{name: "cancel", run: func() error { return runner.Cancel(context.Background(), forward) }},
		{name: "close", run: func() error { return runner.closeControl(context.Background()) }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			started := time.Now()
			err := operation.run()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err=%v", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("operation exceeded bound: %s", elapsed)
			}
		})
	}
}

func TestCappedBufferBoundsControlCommandOutput(t *testing.T) {
	var output cappedBuffer
	payload := strings.Repeat("x", maxManagedOutputBytes*2)
	written, err := output.Write([]byte(payload))
	if err != nil || written != len(payload) {
		t.Fatalf("Write=(%d, %v)", written, err)
	}
	if got := len(output.String()); got != maxManagedOutputBytes {
		t.Fatalf("buffer size=%d", got)
	}
}

func TestManagedProcessDiagnosticOutputIsCapped(t *testing.T) {
	process, err := startManagedProcess("/bin/sh", []string{"-c", `i=0; while [ "$i" -lt 20000 ]; do printf 0123456789; i=$((i+1)); done`})
	if err != nil {
		t.Fatal(err)
	}
	<-process.done
	defer process.cleanup()
	info, err := os.Stat(process.output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 64*1024 {
		t.Fatalf("diagnostic output grew without bound: %d bytes", info.Size())
	}
}

func TestCloseRemovesOnlyOwnedStaleControlSocket(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "master.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	identity, err := openControlSocketIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "owned-master-*")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: done, controlPath: path, controlSocket: identity}
	runner := newTestRunner(t, &recordingExecutor{})
	runner.master = process
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned stale control socket survived: %v", err)
	}
	if runner.currentMaster() != nil {
		t.Fatal("terminated master ownership was not cleared")
	}
}

func TestPrivateControlDirectoryIsCreatedAndValidated(t *testing.T) {
	base := t.TempDir()
	directory := filepath.Join(base, "control")
	config := validSSHConfig(t)
	config.ControlPath = filepath.Join(directory, "%C.sock")
	if _, err := NewOpenSSHRunner(config, withoutControlPathLockForTest()); err != nil {
		t.Fatalf("create private ControlPath directory: %v", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("private directory mode=%v", info.Mode())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		t.Fatalf("private directory owner=%v", info.Sys())
	}
}

func TestPrivateControlDirectoryRejectsUnsafeDirectoryAndSymlink(t *testing.T) {
	base := t.TempDir()
	unsafe := filepath.Join(base, "unsafe")
	if err := os.Mkdir(unsafe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	config := validSSHConfig(t)
	config.ControlPath = filepath.Join(unsafe, "%C.sock")
	if _, err := NewOpenSSHRunner(config); err == nil || !strings.Contains(err.Error(), "expected 0700") {
		t.Fatalf("unsafe directory err=%v", err)
	}

	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	config.ControlPath = filepath.Join(link, "%C.sock")
	if _, err := NewOpenSSHRunner(config); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlink directory err=%v", err)
	}

	shared := filepath.Join(base, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	config.ControlPath = filepath.Join(shared, "control", "%C.sock")
	if _, err := NewOpenSSHRunner(config); err == nil || !strings.Contains(err.Error(), "writable without the sticky bit") {
		t.Fatalf("unsafe ancestor err=%v", err)
	}
}

func TestOpenControlSocketIdentityRejectsNonSocketAndSymlink(t *testing.T) {
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	if err := os.WriteFile(regular, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if identity, err := openControlSocketIdentity(regular); err == nil {
		_ = identity.Close()
		t.Fatal("regular file was accepted as a control socket identity")
	}
	listenerPath := filepath.Join(directory, "listener.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: listenerPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	symlink := filepath.Join(directory, "symlink.sock")
	if err := os.Symlink(listenerPath, symlink); err != nil {
		t.Fatal(err)
	}
	if identity, err := openControlSocketIdentity(symlink); err == nil {
		_ = identity.Close()
		t.Fatal("symlink to a socket was accepted as a control socket identity")
	}
}

func TestCloseClearsExitedMasterThatNeverCreatedControlSocket(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "no-socket-master-*")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: done, controlPath: filepath.Join(privateTempDir(t), "never-created.sock")}
	runner := newTestRunner(t, &recordingExecutor{})
	runner.master = process
	if err := runner.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if runner.currentMaster() != nil {
		t.Fatal("exited socketless master was retained")
	}
	if _, err := os.Stat(output.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed output survived cleanup: %v", err)
	}
}

func TestClosePinsAndRemovesSocketCreatedBeforeFastExit(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "fast-exit.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "fast-exit-master-*")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: done, controlPath: path}
	runner := newTestRunner(t, &recordingExecutor{})
	runner.resolvedControlPath = path
	runner.master = process
	if err := runner.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fast-exit stale socket survived: %v", err)
	}
	if runner.currentMaster() != nil {
		t.Fatal("fast-exit master was retained")
	}
}

func TestCloseRefusesToRemoveReplacedControlSocket(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "master.sock")
	first, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	first.SetUnlinkOnClose(false)
	identity, err := openControlSocketIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	output, err := os.CreateTemp(t.TempDir(), "owned-master-*")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: done, controlPath: path, controlSocket: identity}
	runner := newTestRunner(t, &recordingExecutor{})
	runner.master = process
	err = runner.Close(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refuse to remove changed") {
		t.Fatalf("Close err=%v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("replacement control socket was removed: %v", err)
	}
	if runner.currentMaster() != process {
		t.Fatal("ownership was cleared despite ambiguous socket identity")
	}
	process.cleanup()
	if _, err := identity.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("control socket identity descriptor survived cleanup: %v", err)
	}
}

func TestForcedKillRemovesVerifiedOwnedStaleSocket(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "master.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	identity, err := openControlSocketIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "owned-master-*")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: done, controlPath: path, controlSocket: identity, killFn: func() error {
		if err := listener.Close(); err != nil {
			return err
		}
		close(done)
		return nil
	}}
	executor := executorFunc(func(ctx context.Context, _ string, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest(), WithOperationTimeout(25*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	runner.master = process
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket survived confirmed forced kill: %v", err)
	}
	if runner.currentMaster() != nil {
		t.Fatal("confirmed terminated master ownership was not cleared")
	}
}

func TestManagedProcessKillTerminatesForegroundProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "master.sh")
	marker := filepath.Join(dir, "descendant-survived")
	body := "#!/bin/sh\n(sleep 1; printf survived > \"$1\") &\nsleep 5\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	process, err := startManagedProcess(script, []string{marker})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.kill(); err != nil {
		t.Fatal(err)
	}
	<-process.done
	process.cleanup()
	time.Sleep(1200 * time.Millisecond)
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("foreground descendant survived kill: stat err=%v", statErr)
	}
}

func TestRunnerCloseKillsManagedMasterWhenControlSocketIsUnresponsive(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "master.sh")
	marker := filepath.Join(dir, "descendant-survived")
	body := "#!/bin/sh\n(sleep 1; printf survived > \"$1\") &\nsleep 5\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	process, err := startManagedProcess(script, []string{marker})
	if err != nil {
		t.Fatal(err)
	}
	executor := executorFunc(func(ctx context.Context, _ string, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest(), WithOperationTimeout(25*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	runner.master = process
	started := time.Now()
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("close exceeded bound: %s", elapsed)
	}
	if _, statErr := os.Stat(process.output.Name()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("managed diagnostic file survived close: stat err=%v", statErr)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("managed descendant survived close: stat err=%v", statErr)
	}
}

func TestCloseDoesNotWaitForeverWhenManagedExitCannotBeConfirmed(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "stuck-master-*")
	if err != nil {
		t.Fatal(err)
	}
	process := &managedProcess{cmd: &exec.Cmd{}, output: output, done: make(chan struct{})}
	executor := &recordingExecutor{err: errors.New("control socket unavailable")}
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(executor), withoutControlPathLockForTest(), WithOperationTimeout(25*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	runner.master = process
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- runner.Close(ctx) }()
	select {
	case closeErr := <-returned:
		if closeErr == nil || !errors.Is(closeErr, context.DeadlineExceeded) {
			t.Fatalf("close err=%v", closeErr)
		}
	case <-time.After(500 * time.Millisecond):
		close(process.done)
		<-returned
		t.Fatal("Close waited beyond its cleanup deadline")
	}
	if runner.master != process {
		t.Fatal("unconfirmed master ownership was discarded")
	}
	close(process.done)
	process.cleanup()
}

func TestCloseReportsKillFailureAndRetainsOwnership(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "kill-failure-*")
	if err != nil {
		t.Fatal(err)
	}
	killErr := errors.New("kill denied")
	process := &managedProcess{
		cmd:    &exec.Cmd{},
		output: output,
		done:   make(chan struct{}),
		killFn: func() error { return killErr },
	}
	runner, err := NewOpenSSHRunner(validSSHConfig(t), WithExecutor(&recordingExecutor{err: errors.New("control socket unavailable")}), withoutControlPathLockForTest(), WithOperationTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	runner.master = process
	err = runner.Close(context.Background())
	if !errors.Is(err, killErr) {
		t.Fatalf("close err=%v", err)
	}
	if runner.master != process {
		t.Fatal("master ownership was discarded after kill failure")
	}
	close(process.done)
	process.cleanup()
}

func TestResolveControlPathUsesOpenSSHTokensWithExternalConfigDisabled(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "Host gateway.example.com\n  HostName rewritten.example.net\n  ProxyCommand false\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	cfg := validSSHConfig(t)
	cfg.ControlPath = filepath.Join(privateTempDir(t), "%h-%p-%r-%%-%C.sock")
	runner, err := NewOpenSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := filepath.Dir(cfg.ControlPath) + "/gateway.example.com-2222-tunnel-agent-%-"
	if !strings.HasPrefix(runner.resolvedControlPath, wantPrefix) || !strings.HasSuffix(runner.resolvedControlPath, ".sock") {
		t.Fatalf("resolved control path=%q, want prefix %q", runner.resolvedControlPath, wantPrefix)
	}
	hash := strings.TrimSuffix(strings.TrimPrefix(runner.resolvedControlPath, wantPrefix), ".sock")
	if len(hash) != 40 {
		t.Fatalf("%%C hash=%q", hash)
	}
}

func TestEnsureMasterRemovesVerifiedSameUIDStaleSocket(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "stale.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	stale := errors.New("Control socket connect(" + path + "): Connection refused")
	executor := &recordingExecutor{errs: []error{stale, stale, errControlMasterAbsent, nil, nil}}
	cfg := validSSHConfig(t)
	cfg.ControlPath = path
	runner, err := NewOpenSSHRunner(cfg, WithExecutor(executor), withoutControlPathLockForTest())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.EnsureMaster(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket was not removed: %v", err)
	}
	if len(executor.calls) != 5 {
		t.Fatalf("calls=%#v", executor.calls)
	}
}

func TestEnsureMasterRefusesConnectionRefusedNonSocket(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "control.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validSSHConfig(t)
	cfg.ControlPath = path
	runner, err := NewOpenSSHRunner(cfg, WithExecutor(&recordingExecutor{err: errors.New("Control socket connect(" + path + "): Connection refused")}), withoutControlPathLockForTest())
	if err != nil {
		t.Fatal(err)
	}
	err = runner.EnsureMaster(context.Background())
	if err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("protected path changed: %v", err)
	}
}

func TestManagedProcessWaitDelayBoundsInheritedOutputPipe(t *testing.T) {
	pidFile := filepath.Join(privateTempDir(t), "child.pid")
	script := "(setsid sh -c 'echo $$ > " + pidFile + "; sleep 30' >&2 2>&2 </dev/null &) ; exit 0"
	process, err := startManagedProcess("/bin/sh", []string{"-c", script})
	if err != nil {
		t.Fatal(err)
	}
	defer process.cleanup()
	var childPID int
	defer func() {
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for childPID == 0 && time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if childPID == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	select {
	case <-process.done:
		if !errors.Is(process.waitError(), exec.ErrWaitDelay) {
			t.Fatalf("wait error=%v", process.waitError())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("managed process Wait blocked on inherited output pipe")
	}
}

func TestWithExecutorKeepsControlPathLockEnabled(t *testing.T) {
	cfg := validSSHConfig(t)
	cfg.ControlPath = filepath.Join(privateTempDir(t), "master-%C.sock")
	first, err := NewOpenSSHRunner(cfg, WithExecutor(&recordingExecutor{}))
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := first.acquireControlPathLock()
	if err != nil || !acquired {
		t.Fatalf("first lock acquired=%v err=%v", acquired, err)
	}
	defer first.releaseControlPathLock()

	second, err := NewOpenSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.acquireControlPathLock(); err == nil {
		t.Fatal("WithExecutor unexpectedly disabled ControlPath locking")
	}
}

func TestEnsureMasterRetainsCrossProcessLockWhenUnmanagedExitIsUnconfirmed(t *testing.T) {
	cfg := validSSHConfig(t)
	cfg.ControlPath = filepath.Join(privateTempDir(t), "master-%C.sock")
	runner, err := NewOpenSSHRunner(cfg, WithExecutor(&recordingExecutor{errs: []error{nil, nil, context.DeadlineExceeded}}))
	if err != nil {
		t.Fatal(err)
	}
	defer runner.releaseControlPathLock()
	if err := runner.EnsureMaster(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnsureMaster err=%v", err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestControlPathLockHelper$")
	command.Env = append(os.Environ(), "PORTLOOM_CONTROL_LOCK_HELPER="+cfg.ControlPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock helper acquired ownership after unconfirmed unmanaged exit: %v: %s", err, output)
	}
}

func TestCloseRetainsCrossProcessLockWhenUnmanagedStateIsUnknown(t *testing.T) {
	cfg := validSSHConfig(t)
	cfg.ControlPath = filepath.Join(privateTempDir(t), "master-%C.sock")
	runner, err := NewOpenSSHRunner(cfg, WithExecutor(&recordingExecutor{err: context.DeadlineExceeded}))
	if err != nil {
		t.Fatal(err)
	}
	defer runner.releaseControlPathLock()
	if err := runner.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err=%v", err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestControlPathLockHelper$")
	command.Env = append(os.Environ(), "PORTLOOM_CONTROL_LOCK_HELPER="+cfg.ControlPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock helper acquired ownership after unknown unmanaged state: %v: %s", err, output)
	}
}

func TestEnsureMasterRetainsCrossProcessLockWhenManagedTerminationIsUnconfirmed(t *testing.T) {
	cfg := validSSHConfig(t)
	cfg.ControlPath = filepath.Join(privateTempDir(t), "master-%C.sock")
	killErr := errors.New("kill denied")
	output, err := os.CreateTemp(t.TempDir(), "unconfirmed-master-*")
	if err != nil {
		t.Fatal(err)
	}
	process := &managedProcess{
		cmd:    &exec.Cmd{},
		output: output,
		done:   make(chan struct{}),
		killFn: func() error { return killErr },
	}
	runner, err := NewOpenSSHRunner(
		cfg,
		WithExecutor(&recordingExecutor{err: errors.New("control socket unavailable")}),
		WithOperationTimeout(10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	runner.master = process
	if err := runner.EnsureMaster(context.Background()); !errors.Is(err, killErr) {
		t.Fatalf("EnsureMaster err=%v", err)
	}
	if runner.master != process {
		t.Fatal("unconfirmed master ownership was discarded")
	}

	command := exec.Command(os.Args[0], "-test.run=^TestControlPathLockHelper$")
	command.Env = append(os.Environ(), "PORTLOOM_CONTROL_LOCK_HELPER="+cfg.ControlPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock helper acquired ownership after failed EnsureMaster: %v: %s", err, output)
	}

	close(process.done)
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	process.cleanup()
	second, err := NewOpenSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := second.acquireControlPathLock()
	if err != nil || !acquired {
		t.Fatalf("lock was not reusable after confirmed Close: acquired=%v err=%v", acquired, err)
	}
	if err := second.releaseControlPathLock(); err != nil {
		t.Fatal(err)
	}
}

func TestControlPathLockHelper(t *testing.T) {
	controlPath := os.Getenv("PORTLOOM_CONTROL_LOCK_HELPER")
	if controlPath == "" {
		return
	}
	cfg := validSSHConfig(t)
	cfg.ControlPath = controlPath
	runner, err := NewOpenSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := runner.acquireControlPathLock()
	if err == nil {
		if acquired {
			_ = runner.releaseControlPathLock()
		}
		t.Fatal("cross-process ControlPath lock unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "owned by another PortLoom process") {
		t.Fatalf("unexpected lock error: %v", err)
	}
}

func TestControlPathLockIsExclusiveAcrossProcessesAndReusable(t *testing.T) {
	cfg := validSSHConfig(t)
	cfg.ControlPath = filepath.Join(privateTempDir(t), "master-%C.sock")
	first, err := NewOpenSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := first.acquireControlPathLock()
	if err != nil || !acquired {
		t.Fatalf("first lock acquired=%v err=%v", acquired, err)
	}
	defer first.releaseControlPathLock()

	command := exec.Command(os.Args[0], "-test.run=^TestControlPathLockHelper$")
	command.Env = append(os.Environ(), "PORTLOOM_CONTROL_LOCK_HELPER="+cfg.ControlPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock helper: %v: %s", err, output)
	}

	if err := first.releaseControlPathLock(); err != nil {
		t.Fatal(err)
	}
	second, err := NewOpenSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err = second.acquireControlPathLock()
	if err != nil || !acquired {
		t.Fatalf("reused lock acquired=%v err=%v", acquired, err)
	}
	if err := second.releaseControlPathLock(); err != nil {
		t.Fatal(err)
	}
}

func TestControlPathLockRefusesSymlink(t *testing.T) {
	cfg := validSSHConfig(t)
	cfg.ControlPath = filepath.Join(privateTempDir(t), "master.sock")
	target := filepath.Join(privateTempDir(t), "target")
	if err := os.WriteFile(target, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, cfg.ControlPath+".portloom.lock"); err != nil {
		t.Fatal(err)
	}
	runner, err := NewOpenSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.acquireControlPathLock(); err == nil {
		t.Fatal("symlink ControlPath lock was accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "protected" {
		t.Fatalf("symlink target changed: %q err=%v", data, err)
	}
}

func TestConfigRejectsArgumentInjectionAndUnsafeForward(t *testing.T) {
	cases := []Config{func() Config { c := validSSHConfig(t); c.User = "root -oProxyCommand=bad"; return c }(), func() Config { c := validSSHConfig(t); c.Host = "gateway;touch /tmp/pwned"; return c }(), func() Config { c := validSSHConfig(t); c.ControlPath = "relative.sock"; return c }(), func() Config { c := validSSHConfig(t); c.KnownHostsFile = ""; return c }()}
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
