package domain

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"time"
)

type Protocol string

const (
	// ProtocolHTTP publishes a domain (and optional path prefix) over plain
	// HTTP on the public HTTP edge port without requesting a certificate.
	ProtocolHTTP Protocol = "http"
	// ProtocolHTTPS publishes a domain (and optional path prefix) over the
	// public HTTPS edge with an ACME certificate and HTTP-to-HTTPS redirect.
	ProtocolHTTPS Protocol = "https"
	ProtocolTCP   Protocol = "tcp"
	ProtocolUDP   Protocol = "udp"
)

// IsWeb reports whether the protocol is routed by Host/path on the HTTP(S) edge.
func (p Protocol) IsWeb() bool { return p == ProtocolHTTP || p == ProtocolHTTPS }

// IsStream reports whether the protocol publishes a dedicated public port.
func (p Protocol) IsStream() bool { return p == ProtocolTCP || p == ProtocolUDP }

// DefaultPublicPort is the edge port used when a web route leaves PublicPort 0.
func (p Protocol) DefaultPublicPort() int {
	switch p {
	case ProtocolHTTP:
		return 80
	case ProtocolHTTPS:
		return 443
	default:
		return 0
	}
}

type Route struct {
	ID               string    `json:"id"`
	ClientID         string    `json:"client_id"`
	Name             string    `json:"name"`
	Protocol         Protocol  `json:"protocol"`
	Domain           string    `json:"domain,omitempty"`
	PathPrefix       string    `json:"path_prefix,omitempty"`
	StripPath        bool      `json:"strip_path,omitempty"`
	LocalHost        string    `json:"local_host"`
	LocalPort        int       `json:"local_port"`
	RemotePort       int       `json:"remote_port"`
	PublicPort       int       `json:"public_port,omitempty"`
	TunnelGroup      string    `json:"tunnel_group"`
	Enabled          bool      `json:"enabled"`
	DesiredRevision  int64     `json:"desired_revision"`
	ObservedRevision int64     `json:"observed_revision"`
	LocalStatus      string    `json:"local_status"`
	TunnelStatus     string    `json:"tunnel_status"`
	LastError        string    `json:"last_error,omitempty"`
	AgentLastSeenAt  time.Time `json:"agent_last_seen_at,omitempty"`
	PublicStatus     string    `json:"public_status,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const AgentHeartbeatFreshness = 90 * time.Second

// EffectivePublicPort resolves the public port a web route is served on.
func (r Route) EffectivePublicPort() int {
	if r.Protocol.IsWeb() && r.PublicPort == 0 {
		return r.Protocol.DefaultPublicPort()
	}
	return r.PublicPort
}

// MatchesPath reports whether a request path is covered by the route's path
// prefix. An empty prefix matches every path.
func (r Route) MatchesPath(path string) bool {
	if r.PathPrefix == "" {
		return true
	}
	return path == r.PathPrefix || strings.HasPrefix(path, r.PathPrefix+"/")
}

// PublicationReady reports whether a route may currently receive public traffic.
func (r Route) PublicationReady() bool {
	return r.PublicationReadyAt(time.Now().UTC())
}

// PublicationReadyAt is the deterministic form used by tests and status APIs.
func (r Route) PublicationReadyAt(now time.Time) bool {
	if !r.Enabled || r.TunnelStatus != "up" ||
		r.ObservedRevision < r.DesiredRevision || r.AgentLastSeenAt.IsZero() {
		return false
	}
	age := now.Sub(r.AgentLastSeenAt)
	return age >= -5*time.Second && age <= AgentHeartbeatFreshness
}

var hostnameRE = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*)$`)

func NormalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.TrimSuffix(value, ".")
}

// NormalizeDNSHost normalizes a bare DNS hostname and rejects ports, empty
// labels, overlong labels, and other syntax that ACME cannot use as a name.
func NormalizeDNSHost(value string) (string, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	if strings.Contains(value, ":") {
		return "", false
	}
	value = strings.TrimSuffix(value, ".")
	if net.ParseIP(value) != nil {
		return "", false
	}
	if len(value) > 253 || !hostnameRE.MatchString(value) {
		return "", false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 || !strings.ContainsAny(labels[len(labels)-1], "abcdefghijklmnopqrstuvwxyz") {
		return "", false
	}
	return value, true
}

var pathPrefixRE = regexp.MustCompile(`^[A-Za-z0-9._~!$&'()*+,;=:@%/-]*$`)

// NormalizePathPrefix canonicalizes a route path prefix. The empty string (or
// "/") means the whole domain. Valid prefixes start with "/", contain no empty
// or dot-dot segments, and are returned without a trailing slash.
func NormalizePathPrefix(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return "", true
	}
	if !strings.HasPrefix(value, "/") || !pathPrefixRE.MatchString(value) {
		return "", false
	}
	value = strings.TrimSuffix(value, "/")
	for _, segment := range strings.Split(value[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	return value, true
}

// ValidHost reports whether value is an IP address or a syntactically valid DNS hostname.
func ValidHost(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	return len(value) <= 253 && hostnameRE.MatchString(value)
}

func (r *Route) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	r.LocalHost = NormalizeHost(r.LocalHost)
	r.TunnelGroup = strings.TrimSpace(r.TunnelGroup)
	if r.Name == "" {
		return errors.New("name is required")
	}
	switch r.Protocol {
	case ProtocolHTTP, ProtocolHTTPS, ProtocolTCP, ProtocolUDP:
	default:
		return errors.New("protocol must be http, https, tcp, or udp")
	}
	if r.Protocol.IsWeb() {
		normalizedDomain, valid := NormalizeDNSHost(r.Domain)
		if !valid {
			return errors.New("valid domain is required for HTTP/HTTPS route")
		}
		r.Domain = normalizedDomain
		prefix, valid := NormalizePathPrefix(r.PathPrefix)
		if !valid {
			return errors.New("path prefix must start with / and contain no empty or dot segments")
		}
		r.PathPrefix = prefix
		if r.StripPath && r.PathPrefix == "" {
			return errors.New("strip path requires a path prefix")
		}
		if r.PublicPort != 0 && (r.PublicPort < 1 || r.PublicPort > 65535) {
			return errors.New("public port must be between 1 and 65535")
		}
		if r.PublicPort == r.Protocol.DefaultPublicPort() {
			r.PublicPort = 0
		}
	} else {
		r.Domain = NormalizeHost(r.Domain)
		if r.Domain != "" {
			return errors.New("domain is only valid for HTTP/HTTPS route")
		}
		if r.PathPrefix != "" {
			return errors.New("path prefix is only valid for HTTP/HTTPS route")
		}
		if r.StripPath {
			return errors.New("strip path is only valid for HTTP/HTTPS route")
		}
		if r.PublicPort < 1 || r.PublicPort > 65535 {
			return errors.New("public port must be between 1 and 65535 for TCP/UDP route")
		}
	}
	if !ValidHost(r.LocalHost) {
		return errors.New("invalid local host")
	}
	if r.LocalPort < 1 || r.LocalPort > 65535 {
		return errors.New("local port must be between 1 and 65535")
	}
	if r.TunnelGroup == "" {
		if r.Protocol.IsStream() {
			r.TunnelGroup = string(r.Protocol)
		} else {
			r.TunnelGroup = "web"
		}
	}
	return nil
}
