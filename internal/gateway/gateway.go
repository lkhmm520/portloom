package gateway

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/managedssh"
)

type RouteSource interface {
	ListRoutes(context.Context) ([]domain.Route, error)
}

type Handler struct {
	routes                RouteSource
	transport             *http.Transport
	isolatedAgentBindings bool
}

type Option func(*Handler)

func WithIsolatedAgentBindings() Option {
	return func(handler *Handler) { handler.isolatedAgentBindings = true }
}

func New(routes RouteSource, options ...Option) http.Handler {
	handler := &Handler{routes: routes, transport: newTransport()}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	return handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := domain.NormalizeHost(r.Host)
	routes, err := h.routes.ListRoutes(r.Context())
	if err != nil {
		http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
		return
	}
	var selected *domain.Route
	for i := range routes {
		route := &routes[i]
		if route.Enabled && route.Protocol == domain.ProtocolHTTP && route.Domain == host &&
			route.TunnelStatus == "up" && route.ObservedRevision >= route.DesiredRevision {
			selected = route
			break
		}
	}
	if selected == nil {
		http.NotFound(w, r)
		return
	}
	bindAddress := managedssh.LegacyBindAddress
	if h.isolatedAgentBindings {
		bindAddress, err = managedssh.BindAddress(selected.ClientID)
		if err != nil {
			http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	target := &url.URL{Scheme: "http", Host: bindAddress + ":" + strconv.Itoa(selected.RemotePort)}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Host = request.In.Host
			request.SetXForwarded()
		},
		Transport: h.transport,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Printf("gateway upstream failure for host %q: %v", host, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

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
