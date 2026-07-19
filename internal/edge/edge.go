// Package edge implements Portloom's built-in public HTTP(S) ingress.
package edge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/gateway"
)

// CertificateDomainSource is the minimal read-only route lookup used to
// authorize certificates and redirects for HTTPS routes.
type CertificateDomainSource interface {
	HTTPSDomainEnabled(context.Context, string) (bool, error)
}

const (
	managementRequestTimeout = 30 * time.Second
	managementMaxBodyBytes   = 1 << 20
)

// NewRouter dispatches requests on the public HTTPS edge. The management
// hostname reaches the control plane unless a path-prefix route claims the
// request first; every other hostname is served by the gateway. Gateway
// remains responsible for checking that a route is enabled, converged, and
// has an active tunnel.
func NewRouter(publicHost string, control http.Handler, gatewayHandler *gateway.Handler, httpsAddr string) (http.Handler, error) {
	publicHost = domain.NormalizeHost(publicHost)
	if publicHost == "" {
		return nil, errors.New("public host is required")
	}
	if control == nil || gatewayHandler == nil {
		return nil, errors.New("control and gateway handlers are required")
	}
	httpsPort, err := listenerPort(httpsAddr)
	if err != nil {
		return nil, fmt.Errorf("parse HTTPS listener address: %w", err)
	}
	edgeInfo := gateway.Edge{Scheme: "https", Port: httpsPort}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(gateway.WithEdge(r.Context(), edgeInfo))
		if domain.NormalizeHost(r.Host) == publicHost {
			// Path-prefix routes may share the management domain, but the
			// control plane keeps /, /api, /assets, and /healthz.
			if gatewayHandler.ServeIfMatch(w, r) {
				return
			}
			// Gateway routes may stream for much longer, so the outer edge server
			// has no global body/write deadline. Bound only management traffic
			// here, after Host dispatch and before an API handler reads the body.
			deadline := time.Now().Add(managementRequestTimeout)
			controller := http.NewResponseController(w)
			if err := controller.SetReadDeadline(deadline); err != nil {
				http.Error(w, "management edge unavailable", http.StatusServiceUnavailable)
				return
			}
			if err := controller.SetWriteDeadline(deadline); err != nil {
				http.Error(w, "management edge unavailable", http.StatusServiceUnavailable)
				return
			}
			if r.ContentLength > managementMaxBodyBytes {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, managementMaxBodyBytes)
			control.ServeHTTP(w, r)
			return
		}
		gatewayHandler.ServeHTTP(w, r)
	}), nil
}

// NewHTTPHandler serves the public HTTP edge. The management hostname always
// redirects to HTTPS. Every other hostname is handed to the gateway, which
// serves plain-HTTP routes directly and redirects hosts that only have HTTPS
// routes; unknown names return 404 so a wildcard DNS record cannot become an
// open redirect. ACME HTTP-01 challenge handling is layered in front of this
// handler by Server.
func NewHTTPHandler(publicHost, httpAddr, httpsAddr string, gatewayHandler http.Handler) (http.Handler, error) {
	publicHost = domain.NormalizeHost(publicHost)
	if publicHost == "" {
		return nil, errors.New("public host is required")
	}
	if gatewayHandler == nil {
		return nil, errors.New("gateway handler is required")
	}
	httpPort, err := listenerPort(httpAddr)
	if err != nil {
		return nil, fmt.Errorf("parse HTTP listener address: %w", err)
	}
	httpsPort, err := listenerPort(httpsAddr)
	if err != nil {
		return nil, fmt.Errorf("parse HTTPS listener address: %w", err)
	}
	edgeInfo := gateway.Edge{Scheme: "http", Port: httpPort}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := domain.NormalizeHost(r.Host)
		if host == "" {
			http.NotFound(w, r)
			return
		}
		if host == publicHost {
			authority := host
			if httpsPort != 443 {
				authority = net.JoinHostPort(host, strconv.Itoa(httpsPort))
			}
			http.Redirect(w, r, "https://"+authority+r.URL.RequestURI(), http.StatusPermanentRedirect)
			return
		}
		gatewayHandler.ServeHTTP(w, r.WithContext(gateway.WithEdge(r.Context(), edgeInfo)))
	}), nil
}

func listenerPort(address string) (int, error) {
	_, raw, err := net.SplitHostPort(address)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("listener port must be within 1..65535")
	}
	return port, nil
}
