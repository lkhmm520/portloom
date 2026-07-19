package gateway

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/managedssh"
)

type RouteSource interface {
	ListRoutes(context.Context) ([]domain.Route, error)
}

// Edge describes the listener a request entered through. The zero value is
// the legacy scheme-agnostic gateway listener that matches default-port
// HTTP and HTTPS routes alike.
type Edge struct {
	// Scheme is "http", "https", or "" for the legacy listener.
	Scheme string
	// Port is the public listener port.
	Port int
	// Default marks the primary HTTP/HTTPS edge listeners. Routes without an
	// explicit public port are served by the primary listeners regardless of
	// which ports those are configured on; routes with an explicit port are
	// served by the matching extra-port listener.
	Default bool
}

type contextKey struct{}

// WithEdge annotates a request context with the listener it arrived on.
func WithEdge(ctx context.Context, edge Edge) context.Context {
	return context.WithValue(ctx, contextKey{}, edge)
}

func edgeFromContext(ctx context.Context) Edge {
	if edge, ok := ctx.Value(contextKey{}).(Edge); ok {
		return edge
	}
	return Edge{}
}

type Handler struct {
	routes                RouteSource
	transport             *http.Transport
	isolatedAgentBindings bool
	httpsRedirectPort     int
	observer              TrafficObserver
}

// TrafficObserver receives per-route traffic accounting from the gateway.
type TrafficObserver interface {
	ObserveHTTP(routeID string, requestBytes, responseBytes int64)
}

type Option func(*Handler)

func WithIsolatedAgentBindings() Option {
	return func(handler *Handler) { handler.isolatedAgentBindings = true }
}

// WithHTTPSRedirect makes unmatched plain-HTTP requests for hosts that have an
// enabled HTTPS route redirect to HTTPS on the given port instead of a 404.
func WithHTTPSRedirect(port int) Option {
	return func(handler *Handler) { handler.httpsRedirectPort = port }
}

// WithTrafficObserver wires per-route traffic accounting.
func WithTrafficObserver(observer TrafficObserver) Option {
	return func(handler *Handler) { handler.observer = observer }
}

func New(routes RouteSource, options ...Option) *Handler {
	handler := &Handler{routes: routes, transport: newTransport()}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	return handler
}

// routeMatches reports whether the route serves requests on the given edge.
func routeMatches(route *domain.Route, edge Edge, host, path string) bool {
	if !route.Protocol.IsWeb() || route.Domain != host {
		return false
	}
	switch edge.Scheme {
	case "":
		// Legacy listener: match default-port routes of either scheme.
		if route.PublicPort != 0 {
			return false
		}
	case string(route.Protocol):
		if route.PublicPort == 0 {
			// Default-port routes belong to the primary edge listeners,
			// wherever those are bound (e.g. a custom --http-port).
			if !edge.Default {
				return false
			}
		} else if edge.Default || edge.Port != route.PublicPort {
			return false
		}
	default:
		return false
	}
	return route.MatchesPath(path)
}

// selectRoute returns the ready route with the longest matching path prefix.
func selectRoute(routes []domain.Route, edge Edge, host, path string) *domain.Route {
	var selected *domain.Route
	for i := range routes {
		route := &routes[i]
		if !routeMatches(route, edge, host, path) || !route.PublicationReady() {
			continue
		}
		if selected == nil || len(route.PathPrefix) > len(selected.PathPrefix) {
			selected = route
		}
	}
	return selected
}

// ServeIfMatch serves the request when a ready route matches and reports
// whether it did. The edge router uses this to layer path-prefix routes onto
// the management domain without shadowing the control plane.
func (h *Handler) ServeIfMatch(w http.ResponseWriter, r *http.Request) bool {
	host := domain.NormalizeHost(r.Host)
	routes, err := h.routes.ListRoutes(r.Context())
	if err != nil {
		http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
		return true
	}
	selected := selectRoute(routes, edgeFromContext(r.Context()), host, r.URL.Path)
	if selected == nil {
		return false
	}
	h.proxy(w, r, selected)
	return true
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := domain.NormalizeHost(r.Host)
	routes, err := h.routes.ListRoutes(r.Context())
	if err != nil {
		http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
		return
	}
	edge := edgeFromContext(r.Context())
	selected := selectRoute(routes, edge, host, r.URL.Path)
	if selected == nil {
		if edge.Scheme == "http" && h.httpsRedirectPort > 0 && httpsRouteEnabled(routes, host) {
			authority := host
			if h.httpsRedirectPort != 443 {
				authority = net.JoinHostPort(host, strconv.Itoa(h.httpsRedirectPort))
			}
			http.Redirect(w, r, "https://"+authority+r.URL.RequestURI(), http.StatusPermanentRedirect)
			return
		}
		http.NotFound(w, r)
		return
	}
	h.proxy(w, r, selected)
}

func httpsRouteEnabled(routes []domain.Route, host string) bool {
	for i := range routes {
		if routes[i].Protocol == domain.ProtocolHTTPS && routes[i].Domain == host && routes[i].Enabled {
			return true
		}
	}
	return false
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, selected *domain.Route) {
	host := domain.NormalizeHost(r.Host)
	bindAddress := managedssh.LegacyBindAddress
	if h.isolatedAgentBindings {
		var err error
		bindAddress, err = managedssh.BindAddress(selected.ClientID)
		if err != nil {
			http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	target := &url.URL{Scheme: "http", Host: bindAddress + ":" + strconv.Itoa(selected.RemotePort)}
	stripPrefix := ""
	if selected.StripPath && selected.PathPrefix != "" {
		stripPrefix = selected.PathPrefix
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Host = request.In.Host
			if stripPrefix != "" {
				trimmed := strings.TrimPrefix(request.Out.URL.Path, stripPrefix)
				if trimmed == "" {
					trimmed = "/"
				}
				request.Out.URL.Path = trimmed
				request.Out.URL.RawPath = ""
			}
			request.SetXForwarded()
		},
		Transport: h.transport,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Printf("gateway upstream failure for host %q: %v", host, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	if h.observer != nil {
		counted := &countingResponseWriter{ResponseWriter: w}
		body := &countingReadCloser{ReadCloser: r.Body}
		r.Body = body
		defer func() { h.observer.ObserveHTTP(selected.ID, body.n, counted.n) }()
		proxy.ServeHTTP(counted, r)
		return
	}
	proxy.ServeHTTP(w, r)
}

type countingResponseWriter struct {
	http.ResponseWriter
	n int64
}

func (w *countingResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.n += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer for
// flushing and hijacking (websockets, streaming).
func (w *countingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type countingReadCloser struct {
	ReadCloser interface {
		Read([]byte) (int, error)
		Close() error
	}
	n int64
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.n += int64(n)
	return n, err
}

func (r *countingReadCloser) Close() error { return r.ReadCloser.Close() }

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&netDialer).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

var netDialer = net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
