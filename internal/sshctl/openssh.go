package sshctl

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	SSHExecutable             = "/usr/bin/ssh"
	managedGracePeriod        = 250 * time.Millisecond
	managedKillConfirmTimeout = time.Second
	maxManagedOutputBytes     = 64 * 1024
)

var (
	userPattern            = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,31}$`)
	hostPattern            = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*)$`)
	errControlMasterAbsent = errors.New("SSH ControlMaster is absent")
)

type controlMasterState uint8

const (
	controlMasterUnknown controlMasterState = iota
	controlMasterAbsent
	controlMasterStale
	controlMasterRunning
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
			r.useExecutorForMaster = true
		}
	}
}

func WithMasterStartupTimeout(timeout time.Duration) Option {
	return func(r *OpenSSHRunner) {
		if timeout > 0 {
			r.masterStartupTimeout = timeout
		}
	}
}

func WithOperationTimeout(timeout time.Duration) Option {
	return func(r *OpenSSHRunner) {
		if timeout > 0 {
			r.operationTimeout = timeout
		}
	}
}

type OpenSSHRunner struct {
	config                    Config
	executor                  Executor
	masterStartupTimeout      time.Duration
	operationTimeout          time.Duration
	resolvedControlPath       string
	lifecycleGate             chan struct{}
	masterMu                  sync.Mutex
	master                    *managedProcess
	controlLockMu             sync.Mutex
	controlLock               *os.File
	useExecutorForMaster      bool
	disableControlLockForTest bool
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
	resolvedControlPath, err := resolveControlPath(config)
	if err != nil {
		return nil, err
	}
	runner := &OpenSSHRunner{
		config:               config,
		executor:             executorFunc(runCommand),
		masterStartupTimeout: time.Duration(config.ConnectTimeout+5) * time.Second,
		operationTimeout:     time.Duration(config.ConnectTimeout+2) * time.Second,
		resolvedControlPath:  resolvedControlPath,
		lifecycleGate:        make(chan struct{}, 1),
	}
	runner.lifecycleGate <- struct{}{}
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

func resolveControlPath(config Config) (string, error) {
	args := isolatedSSHArgs("-G", "-o", "ControlPath="+config.ControlPath, "-p", strconv.Itoa(config.Port), destination(config))
	output, err := exec.Command(SSHExecutable, args...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = commandError(err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("resolve SSH control path with ssh -G: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), " ")
		if found && key == "controlpath" {
			value = strings.TrimSpace(value)
			if !safeAbsolutePath(value) {
				return "", fmt.Errorf("resolved SSH control path must be a safe absolute path: %q", value)
			}
			return value, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("parse ssh -G control path: %w", err)
	}
	return "", errors.New("ssh -G did not return a control path")
}

func isolatedSSHArgs(args ...string) []string {
	return append([]string{"-F", "/dev/null"}, args...)
}
func safeAbsolutePath(path string) bool {
	return path != "" && filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\r\n") && filepath.Clean(path) == path
}
func validPort(port int) bool { return port >= 1 && port <= 65535 }
func validHost(host string) bool {
	return net.ParseIP(host) != nil || (len(host) <= 253 && hostPattern.MatchString(host))
}

func (r *OpenSSHRunner) acquireLifecycle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.lifecycleGate:
		return nil
	}
}

func (r *OpenSSHRunner) releaseLifecycle() {
	r.lifecycleGate <- struct{}{}
}

func (r *OpenSSHRunner) acquireControlPathLock() (bool, error) {
	if r.disableControlLockForTest {
		return false, nil
	}
	r.controlLockMu.Lock()
	defer r.controlLockMu.Unlock()
	if r.controlLock != nil {
		return false, nil
	}
	lockPath := r.resolvedControlPath + ".portloom.lock"
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return false, fmt.Errorf("open SSH ControlPath lock %q: %w", lockPath, err)
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	if lock == nil {
		_ = syscall.Close(fd)
		return false, fmt.Errorf("open SSH ControlPath lock %q: invalid file descriptor", lockPath)
	}
	closeWithError := func(cause error) (bool, error) {
		return false, errors.Join(cause, lock.Close())
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return closeWithError(fmt.Errorf("inspect SSH ControlPath lock %q: %w", lockPath, err))
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || int(stat.Uid) != os.Geteuid() {
		return closeWithError(fmt.Errorf("SSH ControlPath lock %q must be a regular file owned by uid %d", lockPath, os.Geteuid()))
	}
	if stat.Mode&0o077 != 0 {
		if err := syscall.Fchmod(fd, 0o600); err != nil {
			return closeWithError(fmt.Errorf("secure SSH ControlPath lock %q: %w", lockPath, err))
		}
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return closeWithError(fmt.Errorf("SSH ControlPath %q is owned by another PortLoom process", r.resolvedControlPath))
		}
		return closeWithError(fmt.Errorf("lock SSH ControlPath %q: %w", r.resolvedControlPath, err))
	}
	r.controlLock = lock
	return true, nil
}

func (r *OpenSSHRunner) releaseControlPathLock() error {
	if r.disableControlLockForTest {
		return nil
	}
	r.controlLockMu.Lock()
	defer r.controlLockMu.Unlock()
	if r.controlLock == nil {
		return nil
	}
	lock := r.controlLock
	r.controlLock = nil
	unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	closeErr := lock.Close()
	return errors.Join(unlockErr, closeErr)
}

func (r *OpenSSHRunner) requireControlPathLock() error {
	if r.disableControlLockForTest {
		return nil
	}
	r.controlLockMu.Lock()
	defer r.controlLockMu.Unlock()
	if r.controlLock == nil {
		return errors.New("SSH ControlPath lock is not held")
	}
	return nil
}

func (r *OpenSSHRunner) EnsureMaster(ctx context.Context) (resultErr error) {
	if err := r.acquireLifecycle(ctx); err != nil {
		return fmt.Errorf("acquire SSH lifecycle: %w", err)
	}
	defer r.releaseLifecycle()

	_, err := r.acquireControlPathLock()
	if err != nil {
		return fmt.Errorf("acquire SSH ControlPath ownership: %w", err)
	}

	startupCtx, cancel := context.WithTimeout(ctx, r.masterStartupTimeout)
	defer cancel()

	if master := r.currentMaster(); master != nil {
		if !master.isDone() {
			if err := r.checkMaster(startupCtx); err == nil {
				return nil
			}
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("check owned SSH ControlMaster: %w", err)
		}
		if err := startupCtx.Err(); err != nil {
			return fmt.Errorf("check owned SSH ControlMaster: %w", err)
		}
		if err := r.stopManaged(startupCtx, master); err != nil {
			return fmt.Errorf("replace owned SSH ControlMaster: %w", err)
		}
	}

	state, probeErr := r.probeMaster(startupCtx)
	switch state {
	case controlMasterUnknown:
		return fmt.Errorf("determine existing SSH ControlMaster state: %w", probeErr)
	case controlMasterStale:
		if err := r.removeStaleControlSocket(startupCtx); err != nil {
			return fmt.Errorf("remove stale SSH ControlMaster socket: %w", err)
		}
	case controlMasterRunning:
		if err := r.closeControl(startupCtx); err != nil {
			return fmt.Errorf("replace existing SSH ControlMaster: %w", err)
		}
		if err := r.waitControlStopped(startupCtx); err != nil {
			return fmt.Errorf("confirm existing SSH ControlMaster exit: %w", err)
		}
	case controlMasterAbsent:
	default:
		return fmt.Errorf("invalid SSH ControlMaster state: %d", state)
	}

	cfg := r.config
	common := isolatedSSHArgs("-M", "-N", "-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-o", "ControlPath="+cfg.ControlPath, "-o", "ExitOnForwardFailure=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+cfg.KnownHostsFile, "-o", "ConnectTimeout="+strconv.Itoa(cfg.ConnectTimeout), "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-i", cfg.IdentityFile, "-p", strconv.Itoa(cfg.Port), destination(cfg))
	if r.useExecutorForMaster {
		if err := r.executor.Run(startupCtx, SSHExecutable, common); err != nil {
			return fmt.Errorf("start SSH ControlMaster: %w", err)
		}
		if err := r.checkMaster(startupCtx); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), r.operationTimeout)
			defer cleanupCancel()
			cleanupErr := r.closeControl(cleanupCtx)
			if cleanupErr == nil {
				cleanupErr = r.waitControlStopped(cleanupCtx)
			}
			return errors.Join(fmt.Errorf("verify SSH ControlMaster readiness: %w", err), cleanupErr)
		}
		return nil
	}

	master, err := startManagedProcess(SSHExecutable, common)
	if err != nil {
		return fmt.Errorf("start SSH ControlMaster: %w", err)
	}
	master.mu.Lock()
	master.controlPath = r.resolvedControlPath
	master.mu.Unlock()
	r.setManagedProcess(master)

	var readinessErr error
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := r.checkMaster(startupCtx); err == nil {
			if err := r.recordManagedControlSocket(master); err == nil {
				return nil
			} else {
				readinessErr = err
			}
		} else {
			readinessErr = err
			_ = r.recordManagedControlSocket(master)
		}
		select {
		case <-master.done:
			waitErr := master.waitError()
			output := master.errorOutput()
			_ = r.recordManagedControlSocket(master)
			cleanupErr := r.cleanupManagedControlSocket(master)
			if cleanupErr == nil {
				r.clearManagedProcess(master)
			}
			return errors.Join(fmt.Errorf("start SSH ControlMaster: %w: %s", waitErr, output), cleanupErr)
		case <-startupCtx.Done():
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), r.operationTimeout)
			cleanupErr := r.stopManaged(cleanupCtx, master)
			cleanupCancel()
			return errors.Join(fmt.Errorf("verify SSH ControlMaster readiness: %w", readinessErr), startupCtx.Err(), cleanupErr)
		case <-ticker.C:
		}
	}
}

func (r *OpenSSHRunner) CheckMaster(ctx context.Context) (resultErr error) {
	if err := r.acquireLifecycle(ctx); err != nil {
		return fmt.Errorf("acquire SSH lifecycle: %w", err)
	}
	defer r.releaseLifecycle()
	lockAcquired, err := r.acquireControlPathLock()
	if err != nil {
		return fmt.Errorf("acquire SSH ControlPath ownership: %w", err)
	}
	if lockAcquired {
		defer func() { resultErr = errors.Join(resultErr, r.releaseControlPathLock()) }()
	}
	return r.checkMaster(ctx)
}

func (r *OpenSSHRunner) checkMaster(ctx context.Context) error {
	state, err := r.probeMaster(ctx)
	if state == controlMasterRunning {
		return nil
	}
	if err != nil {
		return err
	}
	return errControlMasterAbsent
}

func (r *OpenSSHRunner) probeMaster(ctx context.Context) (controlMasterState, error) {
	master := r.currentMaster()
	if master != nil && master.isDone() {
		return controlMasterAbsent, fmt.Errorf("check SSH ControlMaster: process exited: %w", master.waitError())
	}
	opCtx, cancel := r.operationContext(ctx)
	defer cancel()
	args := isolatedSSHArgs("-S", r.config.ControlPath, "-O", "check", "-p", strconv.Itoa(r.config.Port), destination(r.config))
	if err := r.executor.Run(opCtx, SSHExecutable, args); err != nil {
		wrapped := fmt.Errorf("check SSH ControlMaster: %w", err)
		if isControlMasterAbsent(err) {
			return controlMasterAbsent, wrapped
		}
		if isControlMasterStale(err) {
			return controlMasterStale, wrapped
		}
		return controlMasterUnknown, wrapped
	}
	return controlMasterRunning, nil
}

func isControlMasterAbsent(err error) bool {
	if errors.Is(err, errControlMasterAbsent) {
		return true
	}
	message := strings.TrimSpace(strings.ToLower(err.Error()))
	return strings.Contains(message, "control socket connect(") &&
		strings.HasSuffix(message, "): no such file or directory")
}

func isControlMasterStale(err error) bool {
	message := strings.TrimSpace(strings.ToLower(err.Error()))
	return strings.Contains(message, "control socket connect(") &&
		strings.HasSuffix(message, "): connection refused")
}

func (r *OpenSSHRunner) Forward(ctx context.Context, forward Forward) error {
	return r.controlForward(ctx, "forward", forward)
}
func (r *OpenSSHRunner) Cancel(ctx context.Context, forward Forward) error {
	return r.controlForward(ctx, "cancel", forward)
}
func (r *OpenSSHRunner) controlForward(ctx context.Context, operation string, forward Forward) (resultErr error) {
	if err := validateForward(forward); err != nil {
		return err
	}
	if err := r.acquireLifecycle(ctx); err != nil {
		return fmt.Errorf("acquire SSH lifecycle: %w", err)
	}
	defer r.releaseLifecycle()
	lockAcquired, err := r.acquireControlPathLock()
	if err != nil {
		return fmt.Errorf("acquire SSH ControlPath ownership: %w", err)
	}
	defer func() {
		if resultErr != nil && lockAcquired {
			resultErr = errors.Join(resultErr, r.releaseControlPathLock())
		}
	}()
	opCtx, cancel := r.operationContext(ctx)
	defer cancel()
	args := isolatedSSHArgs("-S", r.config.ControlPath, "-O", operation, "-R", formatForward(forward), "-p", strconv.Itoa(r.config.Port), destination(r.config))
	if err := r.executor.Run(opCtx, SSHExecutable, args); err != nil {
		return fmt.Errorf("SSH %s: %w", operation, err)
	}
	return nil
}

func (r *OpenSSHRunner) Close(ctx context.Context) (resultErr error) {
	if err := r.acquireLifecycle(ctx); err != nil {
		return fmt.Errorf("acquire SSH lifecycle: %w", err)
	}
	defer r.releaseLifecycle()
	_, err := r.acquireControlPathLock()
	if err != nil {
		return fmt.Errorf("acquire SSH ControlPath ownership: %w", err)
	}
	defer func() {
		if resultErr == nil {
			resultErr = errors.Join(resultErr, r.releaseControlPathLock())
		}
	}()

	master := r.currentMaster()
	if master == nil {
		cleanupCtx, cancel := context.WithTimeout(ctx, r.operationTimeout)
		defer cancel()
		state, probeErr := r.probeMaster(cleanupCtx)
		switch state {
		case controlMasterUnknown:
			return fmt.Errorf("determine unmanaged SSH ControlMaster state: %w", probeErr)
		case controlMasterAbsent:
			return nil
		case controlMasterStale:
			return r.removeStaleControlSocket(cleanupCtx)
		case controlMasterRunning:
			if err := r.closeControl(cleanupCtx); err != nil {
				return err
			}
			return r.waitControlStopped(cleanupCtx)
		default:
			return fmt.Errorf("invalid SSH ControlMaster state: %d", state)
		}
	}
	return r.stopManaged(ctx, master)
}

func (r *OpenSSHRunner) closeControl(ctx context.Context) error {
	opCtx, cancel := r.operationContext(ctx)
	defer cancel()
	args := isolatedSSHArgs("-S", r.config.ControlPath, "-O", "exit", "-p", strconv.Itoa(r.config.Port), destination(r.config))
	if err := r.executor.Run(opCtx, SSHExecutable, args); err != nil {
		return fmt.Errorf("close SSH ControlMaster: %w", err)
	}
	return nil
}

func (r *OpenSSHRunner) waitControlStopped(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, probeErr := r.probeMaster(ctx)
		switch state {
		case controlMasterAbsent:
			return nil
		case controlMasterStale:
			return r.removeStaleControlSocket(ctx)
		case controlMasterUnknown:
			return fmt.Errorf("confirm SSH ControlMaster exit: %w", probeErr)
		case controlMasterRunning:
		default:
			return fmt.Errorf("invalid SSH ControlMaster state: %d", state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *OpenSSHRunner) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, r.operationTimeout)
}

func (r *OpenSSHRunner) removeStaleControlSocket(ctx context.Context) error {
	if err := r.requireControlPathLock(); err != nil {
		return fmt.Errorf("remove stale SSH ControlMaster socket without ownership: %w", err)
	}
	identity, err := os.Lstat(r.resolvedControlPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale SSH ControlMaster socket: %w", err)
	}
	if identity.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to remove non-socket SSH ControlMaster path %q", r.resolvedControlPath)
	}
	stat, ok := identity.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("refuse to remove SSH ControlMaster socket not owned by uid %d", os.Geteuid())
	}
	state, probeErr := r.probeMaster(ctx)
	if state == controlMasterAbsent {
		if _, err := os.Lstat(r.resolvedControlPath); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("reinspect disappeared SSH ControlMaster socket: %w", err)
		}
		return fmt.Errorf("refuse to remove SSH ControlMaster socket still present after absent probe")
	}
	if state != controlMasterStale {
		return fmt.Errorf("refuse to remove SSH ControlMaster socket after state changed to %d: %w", state, probeErr)
	}
	current, err := os.Lstat(r.resolvedControlPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reinspect stale SSH ControlMaster socket: %w", err)
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(identity, current) {
		return fmt.Errorf("refuse to remove changed SSH ControlMaster socket %q", r.resolvedControlPath)
	}
	if err := os.Remove(r.resolvedControlPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale SSH ControlMaster socket: %w", err)
	}
	state, probeErr = r.probeMaster(ctx)
	if state != controlMasterAbsent {
		return fmt.Errorf("confirm stale SSH ControlMaster socket removal: state %d: %w", state, probeErr)
	}
	return nil
}

func (r *OpenSSHRunner) recordManagedControlSocket(master *managedProcess) error {
	master.mu.Lock()
	if master.controlSocket != nil {
		master.mu.Unlock()
		return nil
	}
	master.mu.Unlock()
	info, err := os.Lstat(r.resolvedControlPath)
	if err != nil {
		return fmt.Errorf("record SSH ControlMaster socket identity: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("record SSH ControlMaster socket identity: %q is not a Unix socket", r.resolvedControlPath)
	}
	master.mu.Lock()
	master.controlPath = r.resolvedControlPath
	master.controlSocket = info
	master.mu.Unlock()
	return nil
}

func (r *OpenSSHRunner) cleanupManagedControlSocket(master *managedProcess) error {
	if err := r.requireControlPathLock(); err != nil {
		return fmt.Errorf("clean owned SSH ControlMaster socket without ownership: %w", err)
	}
	master.mu.Lock()
	path, identity := master.controlPath, master.controlSocket
	master.mu.Unlock()
	if path == "" {
		return nil
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect owned SSH ControlMaster socket: %w", err)
	}
	if identity == nil {
		return errors.New("owned SSH ControlMaster socket identity is unavailable")
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(identity, current) {
		return fmt.Errorf("refuse to remove changed SSH ControlMaster socket %q", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove owned SSH ControlMaster socket: %w", err)
	}
	return nil
}

func (r *OpenSSHRunner) stopManaged(ctx context.Context, master *managedProcess) error {
	if master.isDone() {
		if err := r.cleanupManagedControlSocket(master); err != nil {
			return err
		}
		r.clearManagedProcess(master)
		return nil
	}
	graceTimeout := managedGracePeriod
	if r.operationTimeout < graceTimeout {
		graceTimeout = r.operationTimeout
	}
	graceCtx, graceCancel := context.WithTimeout(ctx, graceTimeout)
	closeErr := r.closeControl(graceCtx)
	if waitManaged(graceCtx, master) {
		graceCancel()
		if err := r.cleanupManagedControlSocket(master); err != nil {
			return err
		}
		r.clearManagedProcess(master)
		return nil
	}
	graceCancel()
	if err := ctx.Err(); err != nil {
		return errors.Join(closeErr, fmt.Errorf("stop SSH ControlMaster canceled before force-stop: %w", err))
	}

	if err := master.kill(); err != nil {
		return errors.Join(closeErr, fmt.Errorf("force-stop SSH ControlMaster: %w", err))
	}
	confirmCtx, confirmCancel := context.WithTimeout(ctx, managedKillConfirmTimeout)
	defer confirmCancel()
	if waitManaged(confirmCtx, master) {
		if err := r.cleanupManagedControlSocket(master); err != nil {
			return err
		}
		r.clearManagedProcess(master)
		return nil
	}
	return errors.Join(closeErr, fmt.Errorf("SSH ControlMaster termination unconfirmed: %w", confirmCtx.Err()))
}

func waitManaged(ctx context.Context, master *managedProcess) bool {
	if master.isDone() {
		return true
	}
	select {
	case <-master.done:
		return true
	case <-ctx.Done():
		return master.isDone()
	}
}

func (r *OpenSSHRunner) currentMaster() *managedProcess {
	r.masterMu.Lock()
	defer r.masterMu.Unlock()
	return r.master
}

func (r *OpenSSHRunner) setManagedProcess(master *managedProcess) {
	r.masterMu.Lock()
	r.master = master
	r.masterMu.Unlock()
}

func (r *OpenSSHRunner) clearManagedProcess(master *managedProcess) {
	r.masterMu.Lock()
	if r.master == master {
		r.master = nil
	}
	r.masterMu.Unlock()
	master.cleanup()
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
	return config.User + "@" + config.Host
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

type cappedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *cappedBuffer) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLength := len(data)
	remaining := maxManagedOutputBytes - w.buffer.Len()
	if remaining <= 0 {
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	_, _ = w.buffer.Write(data)
	return originalLength, nil
}

func (w *cappedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

type cappedFileWriter struct {
	mu        sync.Mutex
	file      *os.File
	remaining int64
}

func (w *cappedFileWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLength := len(data)
	if w.remaining <= 0 {
		return originalLength, nil
	}
	if int64(len(data)) > w.remaining {
		data = data[:w.remaining]
	}
	written, err := w.file.Write(data)
	w.remaining -= int64(written)
	if err != nil {
		return written, err
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	return originalLength, nil
}

type managedProcess struct {
	cmd           *exec.Cmd
	output        *os.File
	done          chan struct{}
	mu            sync.Mutex
	waitErr       error
	killFn        func() error
	controlPath   string
	controlSocket os.FileInfo
	cleanOnce     sync.Once
}

func startManagedProcess(path string, args []string) (*managedProcess, error) {
	output, err := os.CreateTemp("", "portloom-master-output-*")
	if err != nil {
		return nil, fmt.Errorf("create master output: %w", err)
	}
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	outputWriter := &cappedFileWriter{file: output, remaining: maxManagedOutputBytes}
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	process := &managedProcess{cmd: cmd, output: output, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		output.Close()
		os.Remove(output.Name())
		return nil, err
	}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.waitErr = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process, nil
}

func (p *managedProcess) isDone() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *managedProcess) waitError() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waitErr == nil {
		return errors.New("SSH ControlMaster exited unexpectedly")
	}
	return p.waitErr
}

func (p *managedProcess) errorOutput() string {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.output.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(p.output, 1025))
	text := strings.TrimSpace(string(data))
	if len(text) > 1024 {
		text = text[:1024]
	}
	return text
}

func (p *managedProcess) kill() error {
	if p.isDone() {
		return nil
	}
	if p.killFn != nil {
		return p.killFn()
	}
	if p.cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (p *managedProcess) cleanup() {
	p.cleanOnce.Do(func() {
		_ = p.output.Close()
		_ = os.Remove(p.output.Name())
	})
}

func runCommand(ctx context.Context, path string, args []string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	var output cappedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return commandError(err, output.String())
	}
	return nil
}

func commandError(err error, output string) error {
	text := strings.TrimSpace(output)
	if len(text) > 1024 {
		text = text[:1024]
	}
	if text != "" {
		return fmt.Errorf("%w: %s", err, text)
	}
	return err
}
