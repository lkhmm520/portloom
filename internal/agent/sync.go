package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Syncer struct {
	mu               sync.Mutex
	client           ServerClient
	reconciler       StateReconciler
	observedRevision int64
}

func NewSyncer(client ServerClient, reconciler StateReconciler) *Syncer {
	return &Syncer{client: client, reconciler: reconciler}
}
func (s *Syncer) SyncOnce(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	desired, err := s.client.FetchDesired(ctx, s.observedRevision)
	if err != nil {
		return fmt.Errorf("fetch desired state: %w", err)
	}
	observed := s.reconciler.Reconcile(ctx, desired)
	if err := s.client.ReportObserved(ctx, observed); err != nil {
		return fmt.Errorf("report observed state: %w", err)
	}
	s.observedRevision = observed.Revision
	return nil
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
