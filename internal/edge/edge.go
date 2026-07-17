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
)

// HTTPDomainSource is the minimal read-only route lookup used by the public edge.
type HTTPDomainSource interface {
	HTTPDomainEnabled(context.Context, string) (bool, error)
}

const (
	managementRequestTimeout = 30 * time.Second
	managementMaxBodyBytes   = 1 << 20
)

// NewRouter dispatches the configured management hostname to control and every
// other hostname to gateway. Gateway remains responsible for checking that an
// HTTP route is enabled, converged, and has an active tunnel.
func NewRouter(publicHost string, control, gateway http.Handler) (http.Handler, error) {
	publicHost = domain.NormalizeHost(publicHost)
	if publicHost == "" {
		return nil, errors.New("public host is required")
	}
	if control == nil || gateway == nil {
		return nil, errors.New("control and gateway handlers are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if domain.NormalizeHost(r.Host) == publicHost {
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
		gateway.ServeHTTP(w, r)
	}), nil
}

// NewHTTPRedirectHandler redirects only Portloom-owned names to HTTPS. Unknown
// names return 404 so a wildcard DNS record cannot become an open redirect.
// ACME HTTP-01 challenge handling is layered in front of this handler by Server.
func NewHTTPRedirectHandler(publicHost, httpsAddr string, routes HTTPDomainSource) (http.Handler, error) {
	publicHost = domain.NormalizeHost(publicHost)
	if publicHost == "" {
		return nil, errors.New("public host is required")
	}
	_, httpsPort, err := net.SplitHostPort(httpsAddr)
	if err != nil {
		return nil, fmt.Errorf("parse HTTPS listener address: %w", err)
	}
	portNumber, err := strconv.Atoi(httpsPort)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, errors.New("HTTPS listener port must be within 1..65535")
	}
	if routes == nil {
		return nil, errors.New("route source is required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := domain.NormalizeHost(r.Host)
		if host == "" {
			http.NotFound(w, r)
			return
		}
		if host != publicHost {
			enabled, err := routes.HTTPDomainEnabled(r.Context(), host)
			if err != nil {
				http.Error(w, "edge unavailable", http.StatusServiceUnavailable)
				return
			}
			if !enabled {
				http.NotFound(w, r)
				return
			}
		}
		// Strip the incoming HTTP port. Include the configured HTTPS port when
		// it is non-standard so local/NAT-forwarded deployments redirect correctly.
		authority := host
		if httpsPort != "443" {
			authority = net.JoinHostPort(host, httpsPort)
		}
		http.Redirect(w, r, "https://"+authority+r.URL.RequestURI(), http.StatusPermanentRedirect)
	}), nil
}
