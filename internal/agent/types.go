package agent

import (
	"context"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/sysinfo"
)

type Status string

const (
	StatusUp       Status = "up"
	StatusDown     Status = "down"
	StatusDisabled Status = "disabled"
	StatusError    Status = "error"
)

type DesiredState struct {
	Revision int64          `json:"revision"`
	Routes   []domain.Route `json:"routes"`
}
type RouteObservation struct {
	RouteID      string `json:"route_id"`
	LocalStatus  Status `json:"local_status"`
	TunnelStatus Status `json:"tunnel_status"`
	Error        string `json:"error,omitempty"`
}
type ObservedState struct {
	Revision int64              `json:"revision"`
	Routes   []RouteObservation `json:"routes"`
	System   *sysinfo.Stats     `json:"system,omitempty"`
}
type ServerClient interface {
	FetchDesired(context.Context, int64) (DesiredState, error)
	ReportObserved(context.Context, ObservedState) error
}
type StateReconciler interface {
	Reconcile(context.Context, DesiredState) ObservedState
}
