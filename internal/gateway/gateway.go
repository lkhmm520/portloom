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
)

type RouteSource interface {
	ListRoutes(context.Context) ([]domain.Route, error)
}

type Handler struct {
	routes    RouteSource
	transport *http.Transport
}

func New(routes RouteSource) http.Handler {
	return &Handler{routes: routes, transport: newTransport()}
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
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(selected.RemotePort)}
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
