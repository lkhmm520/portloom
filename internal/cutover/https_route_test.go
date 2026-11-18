package cutover

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/gateway"
)

type probeRouteSource []domain.Route

func (s probeRouteSource) ListRoutes(context.Context) ([]domain.Route, error) { return s, nil }

func TestHTTPSProbeRejectsParentFallback(t *testing.T) {
	const body = "identical canary response"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(backend.Close)
	port := backend.Listener.Addr().(*net.TCPAddr).Port
	for _, mode := range []ProbeMode{ProbePublic, ProbeIsolated} {
		t.Run(string(mode), func(t *testing.T) {
			host := probeBusiness
			if mode == ProbeIsolated {
				host = probePreview
			}
			parent := domain.Route{ID: "root", Domain: host, Protocol: domain.ProtocolHTTPS, RemotePort: port,
				Enabled: true, TunnelStatus: "up", DesiredRevision: 1, ObservedRevision: 1, AgentLastSeenAt: time.Now()}
			child := parent
			child.ID, child.PathPrefix, child.TunnelStatus = "app", "/app", "down"
			if mode == ProbeIsolated {
				parent.ID, child.ID = "preview-root", "preview-app"
			}
			server, roots := newProbeTLS(t, []string{host}, gateway.New(probeRouteSource{parent, child}, gateway.WithServingNodeID(probeNodeID)))
			addr := netip.MustParseAddrPort(server.Listener.Addr().String())
			root := probeSpec("root", "", "/health", addr, body)
			app := probeSpec("app", "/app", "/app/health", addr, body)
			probe, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{root, app}, PublicRoots: roots, PreviewRoots: roots})
			if err != nil {
				t.Fatal(err)
			}
			probe.dialer.Resolver, _ = probeDNS(t, addr.Addr())
			probe.publicPort = fmt.Sprint(addr.Port())
			err = probe.VerifyHTTPS(context.Background(), probeNode(addr.Addr()), []string{probeBusiness}, []MappingIdentity{root.Route, app.Route}, mode)
			if err == nil || !strings.Contains(err.Error(), `route "app": missing, ambiguous or incorrect serving route evidence`) {
				t.Fatalf("parent fallback satisfied nested canary or failed for the wrong reason: %v", err)
			}
		})
	}
}
