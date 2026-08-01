package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
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
	acceptedDesired  DesiredState
	hasAccepted      bool
	cachedDesired    DesiredState
	cachedDesiredAt  time.Time
	desiredCacheTTL  time.Duration
	now              func() time.Time
	hasCachedDesired bool
}

type SyncerOption func(*Syncer)

// WithSystemStats attaches a resource sampler whose output is reported to the
// server with every observed state.
func WithSystemStats(stats func() sysinfo.Stats) SyncerOption {
	return func(syncer *Syncer) { syncer.stats = stats }
}

// WithDesiredCacheTTL bounds how long cached control-plane intent may be used
// for local tunnel repair while desired-state fetches are temporarily failing.
func WithDesiredCacheTTL(ttl time.Duration) SyncerOption {
	return func(syncer *Syncer) {
		if ttl > 0 {
			syncer.desiredCacheTTL = ttl
		}
	}
}

func withSyncerClock(now func() time.Time) SyncerOption {
	return func(syncer *Syncer) {
		if now != nil {
			syncer.now = now
		}
	}
}

func NewSyncer(client ServerClient, reconciler StateReconciler, options ...SyncerOption) *Syncer {
	syncer := &Syncer{
		client: client, reconciler: reconciler,
		desiredCacheTTL: 2 * time.Minute,
		now:             time.Now,
	}
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
		fetchErr := fmt.Errorf("fetch desired state: %w", err)
		recoverable := recoverableFetchError(err)
		if recoverable && s.hasCachedDesired && ctx.Err() == nil {
			age := s.now().Sub(s.cachedDesiredAt)
			if age < 0 {
				age = 0
			}
			if age <= s.desiredCacheTTL {
				observed := s.reconciler.Reconcile(ctx, s.cachedDesired)
				return errors.Join(fetchErr, cachedReconcileError(observed))
			}
			s.cachedDesired = DesiredState{}
			s.cachedDesiredAt = time.Time{}
			s.hasCachedDesired = false
			return errors.Join(fetchErr, fmt.Errorf("cached desired state expired after %s", age.Round(time.Second)))
		}
		if !recoverable && ctx.Err() == nil {
			s.cachedDesired = DesiredState{}
			s.cachedDesiredAt = time.Time{}
			s.hasCachedDesired = false
		}
		return fetchErr
	}
	if err := s.acceptDesired(desired); err != nil {
		return err
	}
	s.cachedDesired = desired
	s.cachedDesiredAt = s.now()
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

func normalizedDesiredExecution(desired DesiredState) DesiredState {
	desired.Routes = slices.Clone(desired.Routes)
	for index := range desired.Routes {
		route := &desired.Routes[index]
		route.ObservedRevision = 0
		route.LocalStatus = ""
		route.TunnelStatus = ""
		route.LastError = ""
		route.AgentLastSeenAt = time.Time{}
		route.PublicStatus = ""
		route.CreatedAt = time.Time{}
		route.UpdatedAt = time.Time{}
	}
	return desired
}

func (s *Syncer) acceptDesired(desired DesiredState) error {
	if desired.Revision < 0 {
		return fmt.Errorf("desired revision must not be negative: %d", desired.Revision)
	}
	normalized := normalizedDesiredExecution(desired)
	if s.hasAccepted {
		switch {
		case desired.Revision < s.acceptedDesired.Revision:
			return fmt.Errorf("desired revision rollback from %d to %d", s.acceptedDesired.Revision, desired.Revision)
		case desired.Revision == s.acceptedDesired.Revision && !reflect.DeepEqual(normalized, s.acceptedDesired):
			return fmt.Errorf("desired state changed without revision advance: %d", desired.Revision)
		case desired.Revision == s.acceptedDesired.Revision:
			return nil
		}
	}
	s.acceptedDesired = normalized
	s.hasAccepted = true
	return nil
}

func cachedReconcileError(observed ObservedState) error {
	var routeErrors []error
	for _, route := range observed.Routes {
		if route.Error == "" && route.TunnelStatus != StatusError && route.LocalStatus != StatusError {
			continue
		}
		message := route.Error
		if message == "" {
			message = "route reconciliation reported an error status"
		}
		routeErrors = append(routeErrors, fmt.Errorf("cached reconcile route %q: %s", route.RouteID, message))
	}
	return errors.Join(routeErrors...)
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
