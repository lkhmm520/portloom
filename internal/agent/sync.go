package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/lkhmm520/portloom/internal/sysinfo"
)

type Syncer struct {
	mu               sync.Mutex
	client           ServerClient
	reconciler       StateReconciler
	stats            func() sysinfo.Stats
	observedRevision int64
	cachedDesired    DesiredState
	hasCachedDesired bool
}

type SyncerOption func(*Syncer)

// WithSystemStats attaches a resource sampler whose output is reported to the
// server with every observed state.
func WithSystemStats(stats func() sysinfo.Stats) SyncerOption {
	return func(syncer *Syncer) { syncer.stats = stats }
}

func NewSyncer(client ServerClient, reconciler StateReconciler, options ...SyncerOption) *Syncer {
	syncer := &Syncer{client: client, reconciler: reconciler}
	for _, option := range options {
		if option != nil {
			option(syncer)
		}
	}
	return syncer
}
func (s *Syncer) SyncOnce(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	desired, err := s.client.FetchDesired(ctx, s.observedRevision)
	if err != nil {
		recoverable := recoverableFetchError(err)
		if recoverable && s.hasCachedDesired && ctx.Err() == nil {
			s.reconciler.Reconcile(ctx, s.cachedDesired)
		} else if !recoverable && ctx.Err() == nil {
			s.cachedDesired = DesiredState{}
			s.hasCachedDesired = false
		}
		return fmt.Errorf("fetch desired state: %w", err)
	}
	s.cachedDesired = desired
	s.hasCachedDesired = true
	observed := s.reconciler.Reconcile(ctx, desired)
	if s.stats != nil {
		stats := s.stats()
		observed.System = &stats
	}
	if err := s.client.ReportObserved(ctx, observed); err != nil {
		return fmt.Errorf("report observed state: %w", err)
	}
	s.observedRevision = observed.Revision
	return nil
}

func recoverableFetchError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.Temporary()
	}

	var verificationErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certificateErr x509.CertificateInvalidError
	if errors.As(err, &verificationErr) || errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) || errors.As(err, &certificateErr) {
		return false
	}

	for _, temporaryErrno := range []error{
		syscall.ECONNREFUSED,
		syscall.ECONNRESET,
		syscall.ECONNABORTED,
		syscall.ENETDOWN,
		syscall.ENETUNREACH,
		syscall.EHOSTUNREACH,
		syscall.ETIMEDOUT,
		syscall.EPIPE,
	} {
		if errors.Is(err, temporaryErrno) {
			return true
		}
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func (s *Syncer) ObservedRevision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observedRevision
}
func (s *Syncer) Run(ctx context.Context, interval time.Duration, onError ...func(error)) error {
	if interval <= 0 {
		return fmt.Errorf("sync interval must be positive")
	}
	report := func(err error) {
		if err != nil && len(onError) > 0 && onError[0] != nil {
			onError[0](err)
		}
	}
	report(s.SyncOnce(ctx))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			report(s.SyncOnce(ctx))
		}
	}
}
