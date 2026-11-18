package cutover

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
)

const (
	configLimit    = 256 << 10
	tokenLimit     = 4 << 10
	caLimit        = 1 << 20
	inventoryLimit = 4 << 20
)

var ErrInput = errors.New("invalid preflight input")
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

type profile struct {
	URL            string `json:"url"`
	ManagementHost string `json:"management_host"`
	TokenFile      string `json:"token_file"`
	CAFile         string `json:"ca_file,omitempty"`
}

type inventoryClient struct {
	endpoint       url.URL
	token          string
	http           *http.Client
	managementHost string
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("could not read bounded input")
	}
	return data, nil
}

func readLocal(path string, limit int64, private bool) ([]byte, error) {
	// Check before opening (e.g. do not block opening a FIFO) and verify the
	// descriptor still refers to the same regular file before reading bytes.
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || (private && before.Mode().Perm()&0077 != 0) {
		return nil, errors.New("not an accessible regular file with required permissions")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open local file")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || (private && after.Mode().Perm()&0077 != 0) {
		return nil, errors.New("local file changed or has unsafe permissions")
	}
	return readLimited(f, limit)
}

func newInventoryClient(p profile, dir string) (*inventoryClient, error) {
	u, err := url.Parse(p.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || u.ForceQuery || u.RawQuery != "" || strings.Contains(p.URL, "#") || strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("%w: profile URL must be an HTTPS origin without credentials, path, query, or fragment", ErrInput)
	}
	managementHost, ok := domain.NormalizeDNSHost(p.ManagementHost)
	if !ok {
		return nil, fmt.Errorf("%w: profile requires a valid management_host", ErrInput)
	}
	if p.TokenFile == "" {
		return nil, fmt.Errorf("%w: profile requires token_file", ErrInput)
	}
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(dir, path)
	}
	tokenData, err := readLocal(resolve(p.TokenFile), tokenLimit, true)
	token := strings.TrimSpace(string(tokenData))
	if err != nil || !tokenPattern.MatchString(token) {
		return nil, fmt.Errorf("%w: token_file must be a private regular file containing one bearer token", ErrInput)
	}
	var roots *x509.CertPool
	if p.CAFile != "" {
		data, err := readLocal(resolve(p.CAFile), caLimit, false)
		if err != nil {
			return nil, fmt.Errorf("%w: could not read ca_file", ErrInput)
		}
		roots, err = x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("%w: could not load system CA roots", ErrInput)
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("%w: ca_file contains no PEM certificate", ErrInput)
		}
	}
	transport := &http.Transport{
		// Deliberately bypass ambient proxy variables for NAS-local direct access.
		DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    5 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DisableCompression:     true,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &inventoryClient{endpoint: *u, token: token, http: client, managementHost: managementHost}, nil
}

type inventory struct {
	clients []domain.Agent
	routes  []inventoryRoute
}

// Pointer fields distinguish missing or null values from valid zero values.
type inventoryRoute struct {
	domain.Route
	Enabled *bool `json:"enabled"`
}

func (c *inventoryClient) read(ctx context.Context) (inventory, error) {
	var result inventory
	if err := c.get(ctx, "/api/v1/clients", &result.clients); err != nil {
		return inventory{}, err
	}
	if err := c.get(ctx, "/api/v1/routes", &result.routes); err != nil {
		return inventory{}, err
	}
	for _, client := range result.clients {
		if client.ID == "" {
			return inventory{}, errors.New("/api/v1/clients: client missing ID")
		}
	}
	for _, route := range result.routes {
		if route.ID == "" || route.ClientID == "" || route.Protocol == "" || route.Enabled == nil || (route.Protocol.IsWeb() && route.Domain == "") {
			return inventory{}, errors.New("/api/v1/routes: route missing required inventory fields")
		}
	}
	return result, nil
}

func (c *inventoryClient) get(ctx context.Context, path string, dest any) error {
	data, err := c.request(ctx, http.MethodGet, path, nil, http.StatusOK)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "[") || json.Unmarshal(data, dest) != nil {
		return fmt.Errorf("%s: expected a JSON inventory array", path)
	}
	return nil
}

func (c *inventoryClient) request(ctx context.Context, method, path string, body []byte, status int) ([]byte, error) {
	endpoint := c.endpoint
	endpoint.Path = path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid request", path)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("PortLoom-Expected-Management-Host", c.managementHost)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: HTTPS request failed (check connectivity, TLS trust, and timeout)", path)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		if resp.StatusCode == http.StatusConflict && method == http.MethodPost {
			return nil, fmt.Errorf("%s: HTTP %d: %w", path, resp.StatusCode, ErrSwitchConflict)
		}
		return nil, fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	data, err := readLimited(resp.Body, inventoryLimit)
	if err != nil {
		return nil, fmt.Errorf("%s: unreadable or oversized response", path)
	}
	return data, nil
}
