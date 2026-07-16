package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/lkhmm520/portloom/internal/sshctl"
)

type SSHRunner interface {
	EnsureMaster(context.Context) error
	CheckMaster(context.Context) error
	Forward(context.Context, sshctl.Forward) error
	Cancel(context.Context, sshctl.Forward) error
	Close(context.Context) error
}
type Reconciler struct {
	mu               sync.Mutex
	runner           SSHRunner
	checker          HealthChecker
	active           map[string]sshctl.Forward
	masterReady      bool
	observedRevision int64
}

func NewReconciler(runner SSHRunner, checker HealthChecker) *Reconciler {
	return &Reconciler{runner: runner, checker: checker, active: map[string]sshctl.Forward{}}
}
func (r *Reconciler) Reconcile(ctx context.Context, desired DesiredState) ObservedState {
	r.mu.Lock()
	defer r.mu.Unlock()
	observed := ObservedState{Revision: r.observedRevision, Routes: make([]RouteObservation, 0, len(desired.Routes))}
	cancelFailed := false
	seen := map[string]bool{}

	if r.masterReady {
		if err := r.runner.CheckMaster(ctx); err != nil {
			r.masterReady = false
			clear(r.active)
		}
	}

	for _, route := range desired.Routes {
		observation := RouteObservation{RouteID: route.ID}
		if route.ID == "" || seen[route.ID] {
			observation.LocalStatus = StatusError
			observation.TunnelStatus = StatusError
			observation.Error = "route ID is required and must be unique"
			observed.Routes = append(observed.Routes, observation)
			continue
		}
		seen[route.ID] = true
		if !route.Enabled {
			observation.LocalStatus = StatusDisabled
			observation.TunnelStatus = StatusDisabled
			if err := r.cancel(ctx, route.ID); err != nil {
				cancelFailed = true
				observation.TunnelStatus = StatusError
				observation.Error = err.Error()
			}
			observed.Routes = append(observed.Routes, observation)
			continue
		}
		if err := route.Validate(); err != nil {
			observation.LocalStatus = StatusError
			observation.TunnelStatus = StatusError
			observation.Error = err.Error()
			if cancelErr := r.cancel(ctx, route.ID); cancelErr != nil {
				cancelFailed = true
				observation.Error = joinErrors(err, cancelErr)
			}
			observed.Routes = append(observed.Routes, observation)
			continue
		}
		if route.RemotePort < 1 || route.RemotePort > 65535 {
			observation.LocalStatus = StatusError
			observation.TunnelStatus = StatusError
			observation.Error = "remote port must be between 1 and 65535"
			if cancelErr := r.cancel(ctx, route.ID); cancelErr != nil {
				cancelFailed = true
				observation.Error = joinErrors(fmt.Errorf("%s", observation.Error), cancelErr)
			}
			observed.Routes = append(observed.Routes, observation)
			continue
		}
		forward := sshctl.Forward{BindHost: "127.0.0.1", RemotePort: route.RemotePort, LocalHost: route.LocalHost, LocalPort: route.LocalPort}
		if err := r.checker.Check(ctx, route.LocalHost, route.LocalPort); err != nil {
			observation.LocalStatus = StatusDown
			observation.TunnelStatus = StatusDown
			observation.Error = err.Error()
			if cancelErr := r.cancel(ctx, route.ID); cancelErr != nil {
				cancelFailed = true
				observation.TunnelStatus = StatusError
				observation.Error = joinErrors(err, cancelErr)
			}
			observed.Routes = append(observed.Routes, observation)
			continue
		}
		observation.LocalStatus = StatusUp
		if current, ok := r.active[route.ID]; ok && current == forward {
			observation.TunnelStatus = StatusUp
			observed.Routes = append(observed.Routes, observation)
			continue
		}
		if err := r.cancel(ctx, route.ID); err != nil {
			cancelFailed = true
			observation.TunnelStatus = StatusError
			observation.Error = err.Error()
			observed.Routes = append(observed.Routes, observation)
			continue
		}
		if !r.masterReady {
			if err := r.runner.EnsureMaster(ctx); err != nil {
				observation.TunnelStatus = StatusError
				observation.Error = fmt.Sprintf("ensure SSH master: %v", err)
				observed.Routes = append(observed.Routes, observation)
				continue
			}
			r.masterReady = true
		}
		if err := r.runner.Forward(ctx, forward); err != nil {
			observation.TunnelStatus = StatusError
			observation.Error = err.Error()
		} else {
			r.active[route.ID] = forward
			observation.TunnelStatus = StatusUp
		}
		observed.Routes = append(observed.Routes, observation)
	}
	for id := range r.active {
		if !seen[id] {
			if err := r.cancel(ctx, id); err != nil {
				cancelFailed = true
				observed.Routes = append(observed.Routes, RouteObservation{
					RouteID: id, LocalStatus: StatusDisabled, TunnelStatus: StatusError, Error: err.Error(),
				})
			}
		}
	}
	if !cancelFailed {
		r.observedRevision = desired.Revision
		observed.Revision = desired.Revision
	}
	return observed
}
func (r *Reconciler) cancel(ctx context.Context, id string) error {
	forward, ok := r.active[id]
	if !ok {
		return nil
	}
	if err := r.runner.Cancel(ctx, forward); err != nil {
		return fmt.Errorf("cancel route %s: %w", id, err)
	}
	delete(r.active, id)
	return nil
}
func joinErrors(first, second error) string {
	return fmt.Sprintf("%v; %v", first, second)
}
