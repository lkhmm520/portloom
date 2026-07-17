package agent

import (
	"context"
	"errors"
	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/managedssh"
	"github.com/lkhmm520/portloom/internal/sshctl"
	"net"
	"strconv"
	"testing"
	"time"
)

type fakeRunner struct {
	masters        int
	checks         int
	added, removed []sshctl.Forward
	addErr         error
	checkErr       error
	cancelErr      error
}

func (r *fakeRunner) EnsureMaster(context.Context) error { r.masters++; return nil }
func (r *fakeRunner) CheckMaster(context.Context) error  { r.checks++; return r.checkErr }
func (r *fakeRunner) Forward(_ context.Context, f sshctl.Forward) error {
	r.added = append(r.added, f)
	return r.addErr
}
func (r *fakeRunner) Cancel(_ context.Context, f sshctl.Forward) error {
	r.removed = append(r.removed, f)
	return r.cancelErr
}
func (r *fakeRunner) Close(context.Context) error { return nil }

type fakeChecker struct{ errors map[string]error }

func (c fakeChecker) Check(_ context.Context, h string, p int) error {
	return c.errors[net.JoinHostPort(h, strconv.Itoa(p))]
}
func testRoute() domain.Route {
	return domain.Route{ID: "r1", Name: "service", Protocol: domain.ProtocolHTTP, Domain: "service.example.com", LocalHost: "127.0.0.1", LocalPort: 8080, RemotePort: 14001, Enabled: true}
}
func TestReconcilerStartsHealthyEnabledRouteOnlyOnce(t *testing.T) {
	r := &fakeRunner{}
	x := NewReconciler(r, fakeChecker{})
	d := DesiredState{Revision: 7, Routes: []domain.Route{testRoute()}}
	o := x.Reconcile(context.Background(), d)
	if o.Revision != 7 || len(o.Routes) != 1 {
		t.Fatalf("observed=%#v", o)
	}
	got := o.Routes[0]
	if got.LocalStatus != StatusUp || got.TunnelStatus != StatusUp || got.Error != "" {
		t.Fatalf("route=%#v", got)
	}
	if r.masters != 1 || len(r.added) != 1 {
		t.Fatalf("master=%d added=%#v", r.masters, r.added)
	}
	want := sshctl.Forward{BindHost: "127.0.0.1", RemotePort: 14001, LocalHost: "127.0.0.1", LocalPort: 8080}
	if r.added[0] != want {
		t.Fatalf("forward=%#v", r.added[0])
	}
	x.Reconcile(context.Background(), d)
	if r.masters != 1 || len(r.added) != 1 {
		t.Fatalf("restarted: master=%d added=%d", r.masters, len(r.added))
	}
}
func TestReconcilerCancelsChangedAndDisabledRoutes(t *testing.T) {
	r := &fakeRunner{}
	x := NewReconciler(r, fakeChecker{})
	route := testRoute()
	x.Reconcile(context.Background(), DesiredState{Revision: 1, Routes: []domain.Route{route}})
	route.LocalPort = 9090
	x.Reconcile(context.Background(), DesiredState{Revision: 2, Routes: []domain.Route{route}})
	if len(r.removed) != 1 || len(r.added) != 2 {
		t.Fatalf("removed=%d added=%d", len(r.removed), len(r.added))
	}
	route.Enabled = false
	o := x.Reconcile(context.Background(), DesiredState{Revision: 3, Routes: []domain.Route{route}})
	if len(r.removed) != 2 {
		t.Fatalf("removed=%d", len(r.removed))
	}
	if o.Routes[0].LocalStatus != StatusDisabled || o.Routes[0].TunnelStatus != StatusDisabled {
		t.Fatalf("observed=%#v", o.Routes[0])
	}
}
func TestReconcilerDoesNotForwardUnhealthyOrInvalidRoute(t *testing.T) {
	r := &fakeRunner{}
	route := testRoute()
	x := NewReconciler(r, fakeChecker{errors: map[string]error{"127.0.0.1:8080": errors.New("connection refused")}})
	o := x.Reconcile(context.Background(), DesiredState{Revision: 1, Routes: []domain.Route{route}})
	if len(r.added) != 0 || o.Routes[0].LocalStatus != StatusDown || o.Routes[0].TunnelStatus != StatusDown || o.Routes[0].Error == "" {
		t.Fatalf("observed=%#v", o)
	}
	route.LocalHost = "127.0.0.1;bad"
	o = x.Reconcile(context.Background(), DesiredState{Revision: 2, Routes: []domain.Route{route}})
	if o.Routes[0].TunnelStatus != StatusError {
		t.Fatalf("observed=%#v", o)
	}
}
func TestReconcilerReportsForwardFailure(t *testing.T) {
	r := &fakeRunner{addErr: errors.New("ssh failed")}
	o := NewReconciler(r, fakeChecker{}).Reconcile(context.Background(), DesiredState{Revision: 1, Routes: []domain.Route{testRoute()}})
	if o.Routes[0].LocalStatus != StatusUp || o.Routes[0].TunnelStatus != StatusError || o.Routes[0].Error == "" {
		t.Fatalf("observed=%#v", o)
	}
}
func TestReconcilerRebuildsActiveRoutesWhenControlMasterDisconnects(t *testing.T) {
	r := &fakeRunner{}
	x := NewReconciler(r, fakeChecker{})
	desired := DesiredState{Revision: 1, Routes: []domain.Route{testRoute()}}
	x.Reconcile(context.Background(), desired)
	r.checkErr = errors.New("control socket is gone")
	observed := x.Reconcile(context.Background(), desired)
	if r.checks != 1 || r.masters != 2 || len(r.added) != 2 {
		t.Fatalf("checks=%d masters=%d added=%d", r.checks, r.masters, len(r.added))
	}
	if observed.Routes[0].TunnelStatus != StatusUp {
		t.Fatalf("observed=%#v", observed)
	}
}
func TestReconcilerDoesNotConvergeRevisionWhenCancelFails(t *testing.T) {
	r := &fakeRunner{}
	x := NewReconciler(r, fakeChecker{})
	route := testRoute()
	first := x.Reconcile(context.Background(), DesiredState{Revision: 1, Routes: []domain.Route{route}})
	if first.Revision != 1 {
		t.Fatalf("first revision=%d", first.Revision)
	}
	r.cancelErr = errors.New("master disconnected")
	route.Enabled = false
	failed := x.Reconcile(context.Background(), DesiredState{Revision: 2, Routes: []domain.Route{route}})
	if failed.Revision != 1 {
		t.Fatalf("cancel failure advanced revision to %d", failed.Revision)
	}
	if failed.Routes[0].TunnelStatus != StatusError || failed.Routes[0].Error == "" {
		t.Fatalf("failed observation=%#v", failed.Routes[0])
	}
	r.cancelErr = nil
	converged := x.Reconcile(context.Background(), DesiredState{Revision: 2, Routes: []domain.Route{route}})
	if converged.Revision != 2 || converged.Routes[0].TunnelStatus != StatusDisabled {
		t.Fatalf("converged=%#v", converged)
	}
}
func TestReconcilerReportsFailedCancellationForRemovedRoute(t *testing.T) {
	r := &fakeRunner{}
	x := NewReconciler(r, fakeChecker{})
	x.Reconcile(context.Background(), DesiredState{Revision: 3, Routes: []domain.Route{testRoute()}})
	r.cancelErr = errors.New("cancel failed")
	observed := x.Reconcile(context.Background(), DesiredState{Revision: 4})
	if observed.Revision != 3 || len(observed.Routes) != 1 || observed.Routes[0].RouteID != "r1" || observed.Routes[0].TunnelStatus != StatusError {
		t.Fatalf("observed=%#v", observed)
	}
}
func TestReconcilerUsesConfiguredIsolatedRemoteBindAddress(t *testing.T) {
	runner := &fakeRunner{}
	address, err := managedssh.BindAddress("agent-isolated")
	if err != nil {
		t.Fatal(err)
	}
	reconciler := NewReconciler(runner, fakeChecker{}, WithRemoteBindHost(address))
	reconciler.Reconcile(context.Background(), DesiredState{Revision: 1, Routes: []domain.Route{testRoute()}})
	if len(runner.added) != 1 || runner.added[0].BindHost != address {
		t.Fatalf("forwards=%+v want bind host %s", runner.added, address)
	}
}

func TestTCPHealthCheckerUsesRealLocalTCPConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	checker := TCPHealthChecker{Timeout: time.Second}
	if err := checker.Check(context.Background(), "127.0.0.1", port); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if err := checker.Check(context.Background(), "127.0.0.1", port); err == nil {
		t.Fatal("expected error")
	}
}
