package cutover

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/gateway"
)

const (
	// HTTPSProbeNodeHeader must be set by the trusted serving edge, which must
	// remove any upstream value before inserting its own fixed node ID. An
	// arbitrary application-supplied header is not independent node evidence.
	HTTPSProbeNodeHeader = domain.ServingNodeHeader
	// HTTPSProbeRouteHeader must identify the actual route selected by the edge.
	HTTPSProbeRouteHeader = gateway.ServingRouteHeader
	maxHTTPSProbeBody     = 64 << 10
)

// HTTPSResponseExpectation describes an exact 2xx response and the SHA-256
// of its nonempty, uncompressed application body (at most 64 KiB).
type HTTPSResponseExpectation struct {
	Status int
	SHA256 [sha256.Size]byte
}

// HTTPSRouteProbe binds a read-only canary to one exact enabled mapping on one
// node. Path is a decoded absolute path, without query or fragment. PreviewHost
// is a separate TLS virtual host on PreviewAddress: the operator must provision
// it with network access controls and the same upstream and path-rewrite behavior
// as the business mapping. PreviewRouteID identifies that preview mapping,
// which may differ from the business route ID. This verifier never changes a
// gate and cannot prove preview ACLs or business-host routing in isolated mode.
type HTTPSRouteProbe struct {
	NodeID                          string
	Route                           MappingIdentity
	Path                            string
	PreviewHost                     string
	PreviewRouteID                  string
	PreviewAddress                  netip.AddrPort
	PublicResponse, PreviewResponse HTTPSResponseExpectation
}

// HTTPSProbeConfig is trusted operator configuration, not request input. Nil
// root pools use system trust; non-nil pools replace system trust in their mode.
// The constructor clones the pools. Timeout covers the entire verification
// call (including DNS and response bodies); zero uses 30 seconds.
type HTTPSProbeConfig struct {
	Routes                    []HTTPSRouteProbe
	PublicRoots, PreviewRoots *x509.CertPool
	Timeout                   time.Duration
}

// HTTPSProbe implements direct DNS-only SwitchHTTPS verification. Public mode
// uses the verifier host's ordinary resolver, so run it from the intended public
// vantage point without split DNS or local overrides. It checks the actual TCP
// peer selected for each ordinary request against the approved node addresses.
// When multiple addresses are approved, it also verifies each independently
// using the same business Host and TLS name. It does not verify every DNS
// answer, the inactive node, or all client vantage points.
// A CDN or reverse proxy not included in the direct-node model is unsupported.
// Both modes require live, non-cached canary responses and trustworthy edge
// node and route headers as deployment prerequisites. The probe can be reused concurrently.
type HTTPSProbe struct {
	routes                    map[httpsProbeKey]HTTPSRouteProbe
	publicRoots, previewRoots *x509.CertPool
	timeout                   time.Duration
	dialer                    net.Dialer
	publicPort                string // fixed to 443 in production; ephemeral port in in-package fixtures
}

type httpsProbeKey struct{ node, route string }

var _ SwitchHTTPS = (*HTTPSProbe)(nil)

// NewHTTPSProbe validates and copies the operator's canary configuration.
func NewHTTPSProbe(cfg HTTPSProbeConfig) (*HTTPSProbe, error) {
	if cfg.Timeout < 0 || len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("%w: invalid HTTPS probe timeout or empty route configuration", ErrInput)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	p := &HTTPSProbe{routes: make(map[httpsProbeKey]HTTPSRouteProbe, len(cfg.Routes)), timeout: cfg.Timeout, publicPort: "443"}
	if cfg.PublicRoots != nil {
		p.publicRoots = cfg.PublicRoots.Clone()
	}
	if cfg.PreviewRoots != nil {
		p.previewRoots = cfg.PreviewRoots.Clone()
	}
	business := make(map[string]bool)
	for _, spec := range cfg.Routes {
		business[spec.Route.Host] = true
	}
	previewIDs := make(map[httpsProbeKey]bool, len(cfg.Routes))
	for _, spec := range cfg.Routes {
		key := httpsProbeKey{spec.NodeID, spec.Route.ID}
		addr := spec.PreviewAddress
		if !domain.ValidServingNodeID(spec.NodeID) || !gateway.ValidServingRouteID(spec.Route.ID) ||
			!gateway.ValidServingRouteID(spec.PreviewRouteID) || !spec.Route.Enabled ||
			!probeHost(spec.Route.Host) || !probeHost(spec.PreviewHost) || business[spec.PreviewHost] ||
			!validProbeIP(addr.Addr()) || addr.Port() == 0 ||
			!probePath(spec.Path) || !probePrefix(spec.Route.PathPrefix) || !probeMatchesPath(spec.Route.PathPrefix, spec.Path) ||
			!validProbeResponse(spec.PublicResponse) || !validProbeResponse(spec.PreviewResponse) {
			return nil, fmt.Errorf("%w: invalid HTTPS probe for node %q route %q", ErrInput, key.node, key.route)
		}
		if _, ok := p.routes[key]; ok {
			return nil, fmt.Errorf("%w: duplicate HTTPS probe for node %q route %q", ErrInput, key.node, key.route)
		}
		previewKey := httpsProbeKey{spec.NodeID, spec.PreviewRouteID}
		if previewIDs[previewKey] {
			return nil, fmt.Errorf("%w: duplicate preview route ID for node %q", ErrInput, key.node)
		}
		previewIDs[previewKey] = true
		p.routes[key] = spec
	}
	return p, nil
}

func (p *HTTPSProbe) VerifyHTTPS(ctx context.Context, node SwitchNode, hosts []string, routes []MappingIdentity, mode ProbeMode) error {
	if mode != ProbePublic && mode != ProbeIsolated {
		return fmt.Errorf("%w: unknown HTTPS probe mode %q", ErrInput, mode)
	}
	if p == nil || p.routes == nil {
		return fmt.Errorf("%w: HTTPS probe is not configured", ErrInput)
	}
	jobs, err := p.plan(node, hosts, routes)
	if err != nil {
		return err
	}
	peers := make(map[netip.Addr]bool, len(node.Addresses))
	if mode == ProbePublic {
		if len(node.Addresses) == 0 {
			return fmt.Errorf("%w: missing approved public HTTPS peer addresses", ErrInput)
		}
		for _, ip := range node.Addresses {
			if !validProbeIP(ip) || peers[ip.Unmap()] {
				return fmt.Errorf("%w: invalid or duplicate public HTTPS peer address", ErrInput)
			}
			peers[ip.Unmap()] = true
		}
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.verify(ctx, job, mode, peers, netip.Addr{}); err != nil {
			return fmt.Errorf("%s HTTPS probe for %s route %q: %w", mode, job.Route.Host, job.Route.ID, err)
		}
		// The ordinary request already proves the only peer in a singleton
		// set. With multiple addresses, DNS and TCP fallback can hide failures.
		if mode == ProbePublic && len(peers) > 1 {
			for _, ip := range node.Addresses {
				if err := ctx.Err(); err != nil {
					return err
				}
				ip = ip.Unmap()
				if err := p.verify(ctx, job, mode, peers, ip); err != nil {
					return fmt.Errorf("public HTTPS probe for %s route %q at %s: %w", job.Route.Host, job.Route.ID, net.JoinHostPort(ip.String(), p.publicPort), err)
				}
			}
		}
	}
	return ctx.Err()
}

// Validate complete coverage before any network I/O. A longer enabled prefix
// would either win at the gateway or fall back when unhealthy; neither case
// permits the parent route to be attested by that path.
func (p *HTTPSProbe) plan(node SwitchNode, hosts []string, routes []MappingIdentity) ([]HTTPSRouteProbe, error) {
	if !validSwitchID(node.ID) || !probeHost(node.ManagementHost) || len(hosts) == 0 {
		return nil, fmt.Errorf("%w: invalid HTTPS probe scope", ErrInput)
	}
	scope := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if !probeHost(host) || host == node.ManagementHost || scope[host] {
			return nil, fmt.Errorf("%w: invalid or duplicate HTTPS probe host %q", ErrInput, host)
		}
		scope[host] = true
	}
	ids := make(map[string]bool, len(routes))
	prefixes := make(map[string]map[string]bool, len(hosts))
	covered := make(map[string]bool, len(hosts))
	jobs := make([]HTTPSRouteProbe, 0, len(routes))
	for _, route := range routes {
		if !scope[route.Host] || !validSwitchID(route.ID) || ids[route.ID] || !probePrefix(route.PathPrefix) {
			return nil, fmt.Errorf("%w: invalid, duplicate or out-of-scope HTTPS route %q", ErrInput, route.ID)
		}
		ids[route.ID] = true
		if prefixes[route.Host] == nil {
			prefixes[route.Host] = make(map[string]bool)
		}
		if _, ok := prefixes[route.Host][route.PathPrefix]; ok {
			return nil, fmt.Errorf("%w: duplicate HTTPS mapping for %s prefix %q", ErrInput, route.Host, route.PathPrefix)
		}
		prefixes[route.Host][route.PathPrefix] = route.Enabled
		if !route.Enabled {
			continue
		}
		spec, ok := p.routes[httpsProbeKey{node.ID, route.ID}]
		if !ok || spec.Route != route {
			return nil, fmt.Errorf("%w: missing exact HTTPS probe expectation for %s route %q", ErrInput, route.Host, route.ID)
		}
		if scope[spec.PreviewHost] || spec.PreviewHost == node.ManagementHost {
			return nil, fmt.Errorf("%w: preview hostname overlaps cutover or management scope", ErrInput)
		}
		jobs = append(jobs, spec)
		covered[route.Host] = true
	}
	for _, host := range hosts {
		if !covered[host] {
			return nil, fmt.Errorf("%w: no enabled HTTPS probe coverage for %s", ErrInput, host)
		}
	}
	for _, job := range jobs {
		// Walk only the path's exact ancestor prefixes, not every route on the host.
		for path := job.Path; len(path) > len(job.Route.PathPrefix); {
			if prefixes[job.Route.Host][path] {
				return nil, fmt.Errorf("%w: HTTPS probe path for %s route %q is shadowed by prefix %q", ErrInput, job.Route.Host, job.Route.ID, path)
			}
			path = path[:strings.LastIndexByte(path, '/')]
		}
	}
	return jobs, nil
}

func (p *HTTPSProbe) verify(ctx context.Context, job HTTPSRouteProbe, mode ProbeMode, peers map[netip.Addr]bool, pinnedPeer netip.Addr) error {
	host, roots, want := job.Route.Host, p.publicRoots, job.PublicResponse
	routeID := job.Route.ID
	dialAddress := net.JoinHostPort(host, p.publicPort)
	if mode == ProbeIsolated {
		host, roots, want = job.PreviewHost, p.previewRoots, job.PreviewResponse
		routeID = job.PreviewRouteID
		dialAddress = job.PreviewAddress.String()
	} else if pinnedPeer.IsValid() {
		dialAddress = net.JoinHostPort(pinnedPeer.String(), p.publicPort)
	}
	// Use a fresh socket for each canary. The transport has no proxy, and
	// RoundTrip performs one HTTP transaction without redirect handling.
	deadline, _ := ctx.Deadline() // VerifyHTTPS always installs a deadline.
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, ServerName: host},
		ForceAttemptHTTP2: true, DisableKeepAlives: true, DisableCompression: true,
		MaxResponseHeaderBytes: 32 << 10,
		TLSHandshakeTimeout:    max(time.Nanosecond, time.Until(deadline)),
		DialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
			// net/http can detach dials from the request for connection
			// reuse. This one-shot probe intentionally uses its own context.
			conn, err := p.dialer.DialContext(ctx, network, dialAddress)
			if err != nil {
				return nil, err
			}
			if mode == ProbePublic {
				peer, ok := conn.RemoteAddr().(*net.TCPAddr)
				if !ok || !peers[peer.AddrPort().Addr().Unmap()] ||
					(pinnedPeer.IsValid() && peer.AddrPort().Addr().Unmap() != pinnedPeer) {
					_ = conn.Close()
					return nil, fmt.Errorf("unexpected TCP peer %v for approved node", conn.RemoteAddr())
				}
			}
			return conn, nil
		},
	}
	defer transport.CloseIdleConnections()
	endpoint := url.URL{Scheme: "https", Host: host, Path: job.Path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache, no-store")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want.Status {
		return fmt.Errorf("unexpected HTTP status %d (want %d)", resp.StatusCode, want.Status)
	}
	if values := resp.Header.Values(HTTPSProbeNodeHeader); len(values) != 1 || values[0] != job.NodeID {
		return errors.New("missing, ambiguous or incorrect serving node evidence")
	}
	if values := resp.Header.Values(HTTPSProbeRouteHeader); len(values) != 1 || values[0] != routeID {
		return errors.New("missing, ambiguous or incorrect serving route evidence")
	}
	if enc := resp.Header.Values("Content-Encoding"); len(enc) > 0 && (len(enc) != 1 || !strings.EqualFold(enc[0], "identity")) {
		return errors.New("unexpected HTTPS probe content encoding")
	}
	if resp.ContentLength > maxHTTPSProbeBody {
		return errors.New("HTTPS probe body exceeds limit")
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(resp.Body, maxHTTPSProbeBody+1))
	if err != nil {
		return fmt.Errorf("read HTTPS probe body: %w", err)
	}
	if n > maxHTTPSProbeBody {
		return errors.New("HTTPS probe body exceeds limit")
	}
	if n == 0 || [sha256.Size]byte(digest.Sum(nil)) != want.SHA256 {
		return errors.New("HTTPS probe body digest mismatch")
	}
	return nil
}

func probeHost(host string) bool {
	canonical, ok := domain.NormalizeDNSHost(host)
	return ok && canonical == host
}

func probePath(path string) bool {
	canonical, ok := domain.NormalizePathPrefix(path)
	return ok && strings.HasPrefix(path, "/") && canonical == strings.TrimSuffix(path, "/")
}

func probePrefix(prefix string) bool {
	canonical, ok := domain.NormalizePathPrefix(prefix)
	return ok && canonical == prefix
}

func probeMatchesPath(prefix, path string) bool {
	return (domain.Route{PathPrefix: prefix}).MatchesPath(path)
}

func validProbeIP(ip netip.Addr) bool {
	return ip.Zone() == "" && (ip.Unmap().IsGlobalUnicast() || ip.Unmap().IsLoopback())
}

func validProbeResponse(resp HTTPSResponseExpectation) bool {
	return resp.Status >= 200 && resp.Status < 300 && resp.Status != http.StatusNoContent && resp.Status != http.StatusResetContent &&
		resp.SHA256 != ([sha256.Size]byte{}) && resp.SHA256 != sha256.Sum256(nil)
}
