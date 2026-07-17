package edge

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/lkhmm520/portloom/internal/domain"
	"golang.org/x/crypto/acme/autocert"
)

// NewCertificateManager creates the built-in ACME manager. It deliberately
// authorizes only the management hostname and currently enabled HTTP routes;
// this prevents a wildcard DNS record from being used to obtain certificates
// for arbitrary hostnames.
func NewCertificateManager(cacheDir, publicHost string, routes HTTPDomainSource) (*autocert.Manager, error) {
	if publicHost == "" {
		return nil, errors.New("public host is required")
	}
	var valid bool
	publicHost, valid = domain.NormalizeDNSHost(publicHost)
	if !valid {
		return nil, errors.New("public host must be a valid hostname without a port")
	}
	if routes == nil {
		return nil, errors.New("route source is required")
	}
	if !filepath.IsAbs(cacheDir) {
		return nil, errors.New("certificate cache directory must be absolute")
	}
	return &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(cacheDir),
		HostPolicy: func(ctx context.Context, host string) error {
			var valid bool
			host, valid = domain.NormalizeDNSHost(host)
			if !valid {
				return errors.New("certificate host is invalid")
			}
			if host == publicHost {
				return nil
			}
			enabled, err := routes.HTTPDomainEnabled(ctx, host)
			if err != nil {
				return err
			}
			if !enabled {
				return errors.New("certificate host is not authorized")
			}
			return nil
		},
	}, nil
}
