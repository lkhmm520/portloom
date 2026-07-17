package api

import (
	"context"
	"net/http"

	"github.com/lkhmm520/portloom/internal/domain"
)

type TLSAskRouteSource interface {
	HTTPDomainEnabled(context.Context, string) (bool, error)
}

type tlsAskHandler struct {
	routes TLSAskRouteSource
	config Config
}

// NewTLSAskHandler returns the TLS authorization endpoint without exposing the
// rest of the public/admin API, so callers can mount it on a loopback listener.
func NewTLSAskHandler(routes TLSAskRouteSource, config Config) http.Handler {
	handler := &tlsAskHandler{routes: routes, config: config}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tls/allow", handler.allowTLSCertificate)
	return mux
}

func (h *tlsAskHandler) allowTLSCertificate(w http.ResponseWriter, r *http.Request) {
	if h.config.TLSAskToken == "" || !secureEqual(r.URL.Query().Get("token"), h.config.TLSAskToken) {
		unauthorized(w)
		return
	}
	host, valid := domain.NormalizeDNSHost(r.URL.Query().Get("domain"))
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_domain")
		return
	}
	publicHost, publicHostValid := domain.NormalizeDNSHost(h.config.PublicHost)
	if publicHostValid && host == publicHost {
		w.WriteHeader(http.StatusOK)
		return
	}
	enabled, err := h.routes.HTTPDomainEnabled(r.Context(), host)
	if err != nil {
		internalError(w)
		return
	}
	if enabled {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeError(w, http.StatusForbidden, "domain_not_allowed")
}
