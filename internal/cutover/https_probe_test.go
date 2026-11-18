package cutover

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	probeBusiness = "business.portloom.test"
	probePreview  = "preview.portloom.test"
	probeNodeID   = "fixture-target"
)

func TestHTTPSProbeIsolatedTrustedPreview(t *testing.T) {
	var hits atomic.Int32
	server, roots := newProbeTLS(t, []string{probePreview}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Host != probePreview || r.TLS.ServerName != probePreview || r.Method != http.MethodGet || r.URL.RequestURI() != "/app/health" {
			t.Errorf("unexpected preview request: %s %s %s SNI=%s", r.Method, r.Host, r.URL.RequestURI(), r.TLS.ServerName)
		}
		w.Header().Set("PortLoom-Serving-Node", probeNodeID)
		w.Header().Set(HTTPSProbeRouteHeader, "preview-app")
		fmt.Fprint(w, "app preview ready")
	}))
	addr := netip.MustParseAddrPort(server.Listener.Addr().String())
	spec := probeSpec("app", "/app", "/app/health", addr, "app preview ready")
	probe, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{spec}, PreviewRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.VerifyHTTPS(context.Background(), probeNode(addr.Addr()), []string{probeBusiness}, []MappingIdentity{spec.Route}, ProbeIsolated); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("preview hits = %d", hits.Load())
	}
}

func probeNode(ip netip.Addr) SwitchNode {
	return SwitchNode{ID: probeNodeID, ManagementHost: "control.portloom.test", Addresses: []netip.Addr{ip}}
}

func probeSpec(id, prefix, path string, addr netip.AddrPort, body string) HTTPSRouteProbe {
	expectation := HTTPSResponseExpectation{Status: http.StatusOK, SHA256: sha256.Sum256([]byte(body))}
	return HTTPSRouteProbe{NodeID: probeNodeID, Route: MappingIdentity{ID: id, Host: probeBusiness, PathPrefix: prefix, Enabled: true}, Path: path, PreviewHost: probePreview, PreviewRouteID: "preview-" + id, PreviewAddress: addr, PublicResponse: expectation, PreviewResponse: expectation}
}

func newProbeTLS(t *testing.T, names []string, handler http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	now := time.Now()
	key := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	caKey, serverKey := key(), key()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "PortLoom test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: names, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	sign := func(template, parent *x509.Certificate, public any, signer any) []byte {
		der, err := x509.CreateCertificate(rand.Reader, template, parent, public, signer)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	caDER := sign(ca, ca, &caKey.PublicKey, caKey)
	leafDER := sign(leaf, ca, &serverKey.PublicKey, caKey)
	cert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: serverKey}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, roots
}

// A local UDP DNS server makes the public fixture exercise real name resolution
// and a real TCP peer; only the fixture port differs from production port 443.
func probeDNS(t *testing.T, ip netip.Addr) (*net.Resolver, *atomic.Int32) {
	t.Helper()
	s, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, sender, err := s.ReadFrom(buf)
			if err != nil {
				return
			}
			var q dnsmessage.Message
			if q.Unpack(buf[:n]) != nil || len(q.Questions) != 1 {
				continue
			}
			question := q.Questions[0]
			r := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RecursionAvailable: true}, Questions: q.Questions}
			if question.Name.String() != probeBusiness+"." {
				r.Header.RCode = dnsmessage.RCodeNameError
			} else {
				queries.Add(1)
				if question.Type == dnsmessage.TypeA {
					r.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: ip.As4()}}}
				}
			}
			if data, err := r.Pack(); err == nil {
				_, _ = s.WriteTo(data, sender)
			}
		}
	}()
	t.Cleanup(func() { _ = s.Close(); <-done })
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, s.LocalAddr().String())
	}}, &queries
}

func TestHTTPSProbePublicChecksEveryEnabledPathOverDNS(t *testing.T) {
	var hits atomic.Int32
	server, roots := newProbeTLS(t, []string{probeBusiness}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != probeBusiness || r.TLS.ServerName != probeBusiness {
			t.Errorf("host=%s SNI=%s", r.Host, r.TLS.ServerName)
		}
		hits.Add(1)
		w.Header().Set("PortLoom-Serving-Node", probeNodeID)
		switch r.URL.Path {
		case "/health":
			w.Header().Set(HTTPSProbeRouteHeader, "root")
			fmt.Fprint(w, "parent ready")
		case "/app/health":
			w.Header().Set(HTTPSProbeRouteHeader, "app")
			fmt.Fprint(w, "app ready")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	addr := netip.MustParseAddrPort(server.Listener.Addr().String())
	root := probeSpec("root", "", "/health", addr, "parent ready")
	app := probeSpec("app", "/app", "/app/health", addr, "app ready")
	disabled := MappingIdentity{ID: "disabled", Host: probeBusiness, PathPrefix: "/disabled"}
	probe, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{root, app}, PublicRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	resolver, queries := probeDNS(t, addr.Addr())
	probe.dialer.Resolver, probe.publicPort = resolver, fmt.Sprint(addr.Port())
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	if err := probe.VerifyHTTPS(context.Background(), probeNode(addr.Addr()), []string{probeBusiness}, []MappingIdentity{root.Route, app.Route, disabled}, ProbePublic); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || queries.Load() == 0 {
		t.Fatalf("HTTP hits=%d DNS queries=%d", hits.Load(), queries.Load())
	}
}

func TestHTTPSProbePublicChecksEveryApprovedPeer(t *testing.T) {
	for _, tc := range []struct{ name, body, node, route, want string }{
		{"both healthy", "app ready", probeNodeID, "app", ""},
		{"bad body on secondary", "bad response", probeNodeID, "app", "body digest"},
		{"wrong node on secondary", "app ready", "fixture-source", "app", "serving node"},
		{"wrong route on secondary", "app ready", probeNodeID, "root", "serving route"},
		{"unreachable secondary", "", "", "", "public HTTPS probe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var normalHits, secondaryHits atomic.Int32
			handler := func(hits *atomic.Int32, node, route, body string) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					if r.Host != probeBusiness || r.TLS.ServerName != probeBusiness || r.URL.Path != "/app/health" {
						t.Errorf("unexpected canary Host=%s SNI=%s path=%s", r.Host, r.TLS.ServerName, r.URL.Path)
					}
					w.Header().Set(HTTPSProbeNodeHeader, node)
					w.Header().Set(HTTPSProbeRouteHeader, route)
					fmt.Fprint(w, body)
				}
			}
			primary, roots := newProbeTLS(t, []string{probeBusiness}, handler(&normalHits, probeNodeID, "app", "app ready"))
			addr := netip.MustParseAddrPort(primary.Listener.Addr().String())
			secondary := netip.AddrPortFrom(netip.IPv6Loopback(), addr.Port())
			ln, err := net.Listen("tcp6", secondary.String())
			if err != nil {
				t.Skipf("IPv6 loopback fixture unavailable: %v", err)
			}
			if tc.body == "" {
				_ = ln.Close()
			} else {
				srv := &http.Server{Handler: handler(&secondaryHits, tc.node, tc.route, tc.body), TLSConfig: primary.TLS.Clone()}
				t.Cleanup(func() { _ = srv.Close() })
				go func() { _ = srv.ServeTLS(ln, "", "") }()
			}
			spec := probeSpec("app", "/app", "/app/health", addr, "app ready")
			probe, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{spec}, PublicRoots: roots, Timeout: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			// The ordinary request resolves to the healthy IPv4 peer. It must
			// not obscure a failure on the other expected public address.
			resolver, queries := probeDNS(t, addr.Addr())
			probe.dialer.Resolver, probe.publicPort = resolver, fmt.Sprint(addr.Port())
			node := probeNode(addr.Addr())
			node.Addresses = append(node.Addresses, secondary.Addr())
			err = probe.VerifyHTTPS(context.Background(), node, []string{probeBusiness}, []MappingIdentity{spec.Route}, ProbePublic)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), secondary.String()) {
				t.Fatalf("got %v, want %q identifying %s", err, tc.want, secondary)
			}
			if queries.Load() == 0 || normalHits.Load() == 0 || (tc.body != "" && secondaryHits.Load() != 1) {
				t.Fatalf("DNS queries=%d, primary hits=%d, secondary hits=%d", queries.Load(), normalHits.Load(), secondaryHits.Load())
			}
		})
	}
}

func TestHTTPSProbeRejectsInvalidPublicEvidence(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"untrusted CA", "certificate"}, {"wrong TLS name", "certificate"}, {"wrong IP", "unexpected TCP peer"},
		{"wrong node", "serving node"}, {"duplicate node header", "serving node"}, {"wrong body", "body digest"},
		{"missing route header", "serving route"}, {"duplicate route header", "serving route"}, {"route header list", "serving route"}, {"trailer-only route", "serving route"},
		{"redirect", "HTTP status 307"}, {"oversized", "body exceeds"}, {"cancellation", "context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits, redirected atomic.Int32
			names := []string{probeBusiness}
			if tc.name == "wrong TLS name" {
				names = []string{probePreview}
			}
			server, roots := newProbeTLS(t, names, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					redirected.Add(1)
					return
				}
				hits.Add(1)
				w.Header().Set("PortLoom-Serving-Node", probeNodeID)
				w.Header().Set(HTTPSProbeRouteHeader, "app")
				switch tc.name {
				case "missing route header":
					w.Header().Del(HTTPSProbeRouteHeader)
				case "duplicate route header":
					w.Header().Add(HTTPSProbeRouteHeader, "app")
				case "route header list":
					w.Header().Set(HTTPSProbeRouteHeader, "app, root")
				case "trailer-only route":
					w.Header().Del(HTTPSProbeRouteHeader)
					w.Header().Set(http.TrailerPrefix+HTTPSProbeRouteHeader, "app")
				case "wrong node":
					w.Header().Set("PortLoom-Serving-Node", "fixture-source")
				case "duplicate node header":
					w.Header().Add("PortLoom-Serving-Node", probeNodeID)
				case "redirect":
					http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
					return
				case "wrong body":
					fmt.Fprint(w, "different application")
					return
				case "oversized":
					// No Content-Length: force the streaming limit to do the work.
					w.(http.Flusher).Flush()
					fmt.Fprint(w, strings.Repeat("x", 128<<10))
					return
				}
				fmt.Fprint(w, "app ready")
			}))
			addr := netip.MustParseAddrPort(server.Listener.Addr().String())
			spec := probeSpec("app", "/app", "/app/health", addr, "app ready")
			if tc.name == "untrusted CA" {
				roots = x509.NewCertPool()
			}
			probe, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{spec}, PublicRoots: roots})
			if err != nil {
				t.Fatal(err)
			}
			resolver, _ := probeDNS(t, addr.Addr())
			probe.dialer.Resolver, probe.publicPort = resolver, fmt.Sprint(addr.Port())
			node := probeNode(addr.Addr())
			if tc.name == "wrong IP" {
				node.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.1")}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.name == "cancellation" {
				cancel()
			}
			err = probe.VerifyHTTPS(ctx, node, []string{probeBusiness}, []MappingIdentity{spec.Route}, ProbePublic)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if redirected.Load() != 0 {
				t.Fatal("redirect was followed")
			}
			switch tc.name {
			case "wrong IP", "untrusted CA", "wrong TLS name", "cancellation":
				if hits.Load() != 0 {
					t.Fatal("invalid transport sent an HTTP request")
				}
			}
		})
	}
}

func TestHTTPSProbeRejectsInvalidPlanBeforeDial(t *testing.T) {
	var hits atomic.Int32
	server, roots := newProbeTLS(t, []string{probePreview}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set(HTTPSProbeNodeHeader, probeNodeID)
		w.Header().Set(HTTPSProbeRouteHeader, "preview-root")
		fmt.Fprint(w, "app ready")
	}))
	addr := netip.MustParseAddrPort(server.Listener.Addr().String())
	root := probeSpec("root", "", "/app/health", addr, "app ready")
	app := probeSpec("app", "/app", "/app/health", addr, "app ready")
	probe, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{root, app}, PreviewRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*MappingIdentity)
	}{
		{"missing expectation", func(r *MappingIdentity) { r.ID = "unexpected" }},
		{"outside scope", func(r *MappingIdentity) { r.Host = "other.portloom.test" }},
		{"mapping drift", func(r *MappingIdentity) { r.StripPath = true }},
		{"duplicate route", func(r *MappingIdentity) { *r = root.Route }},
		{"duplicate prefix", func(r *MappingIdentity) { r.PathPrefix = "" }},
		{"shadowed parent", func(r *MappingIdentity) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route := app.Route
			tc.change(&route)
			err := probe.VerifyHTTPS(context.Background(), probeNode(addr.Addr()), []string{probeBusiness}, []MappingIdentity{root.Route, route}, ProbeIsolated)
			if !errors.Is(err, ErrInput) {
				t.Fatalf("got %v, want input error", err)
			}
			if hits.Load() != 0 {
				t.Fatal("invalid plan sent an HTTP request")
			}
		})
	}
	app.Route.Enabled = false
	if err := probe.VerifyHTTPS(context.Background(), probeNode(addr.Addr()), []string{probeBusiness}, []MappingIdentity{app.Route}, ProbeIsolated); !errors.Is(err, ErrInput) {
		t.Fatalf("disabled-only scope: got %v, want input error", err)
	}
	// A disabled child cannot shadow a live parent, nor does it require a canary.
	if err := probe.VerifyHTTPS(context.Background(), probeNode(addr.Addr()), []string{probeBusiness}, []MappingIdentity{root.Route, app.Route}, ProbeIsolated); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("preview hits = %d", hits.Load())
	}
}

func TestHTTPSProbeRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*HTTPSRouteProbe)
	}{
		{"wrong prefix", func(s *HTTPSRouteProbe) { s.Path = "/application/health" }},
		{"path with query", func(s *HTTPSRouteProbe) { s.Path += "?q=value" }},
		{"business preview", func(s *HTTPSRouteProbe) { s.PreviewHost = s.Route.Host }},
		{"missing preview address", func(s *HTTPSRouteProbe) { s.PreviewAddress = netip.AddrPort{} }},
		{"missing preview route ID", func(s *HTTPSRouteProbe) { s.PreviewRouteID = "" }},
		{"ambiguous preview route ID", func(s *HTTPSRouteProbe) { s.PreviewRouteID = "root, app" }},
		{"invalid business route ID", func(s *HTTPSRouteProbe) { s.Route.ID = "app\r\n" }},
		{"missing marker", func(s *HTTPSRouteProbe) { s.PreviewResponse.SHA256 = [sha256.Size]byte{} }},
		{"expected redirect", func(s *HTTPSRouteProbe) { s.PublicResponse.Status = http.StatusTemporaryRedirect }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := probeSpec("app", "/app", "/app/health", netip.MustParseAddrPort("127.0.0.1:443"), "app ready")
			tc.change(&spec)
			if _, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{spec}}); !errors.Is(err, ErrInput) {
				t.Fatalf("got %v, want input error", err)
			}
		})
	}
}

func TestHTTPSProbeRejectsSharedPreviewRoute(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:443")
	root := probeSpec("root", "", "/health", addr, "ready")
	app := probeSpec("app", "/app", "/app/health", addr, "ready")
	app.PreviewRouteID = root.PreviewRouteID
	if _, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{root, app}}); !errors.Is(err, ErrInput) {
		t.Fatalf("got %v, want input error for shared preview route", err)
	}
}

func TestHTTPSProbeDeadlineIncludesStreamingBody(t *testing.T) {
	server, roots := newProbeTLS(t, []string{probePreview}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HTTPSProbeNodeHeader, probeNodeID)
		w.Header().Set(HTTPSProbeRouteHeader, "preview-app")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	addr := netip.MustParseAddrPort(server.Listener.Addr().String())
	spec := probeSpec("app", "/app", "/app/health", addr, "app ready")
	probe, err := NewHTTPSProbe(HTTPSProbeConfig{Routes: []HTTPSRouteProbe{spec}, PreviewRoots: roots, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = probe.VerifyHTTPS(context.Background(), probeNode(addr.Addr()), []string{probeBusiness}, []MappingIdentity{spec.Route}, ProbeIsolated)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "read HTTPS probe body") {
		t.Fatalf("got %v, want body deadline", err)
	}
}
