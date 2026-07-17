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
	ProtocolHTTP Protocol = "http"
	ProtocolTCP  Protocol = "tcp"
)

type Route struct {
	ID               string    `json:"id"`
	ClientID         string    `json:"client_id"`
	Name             string    `json:"name"`
	Protocol         Protocol  `json:"protocol"`
	Domain           string    `json:"domain,omitempty"`
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
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
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
	if r.Protocol != ProtocolHTTP && r.Protocol != ProtocolTCP {
		return errors.New("protocol must be http or tcp")
	}
	if r.Protocol == ProtocolHTTP {
		normalizedDomain, valid := NormalizeDNSHost(r.Domain)
		if !valid {
			return errors.New("valid domain is required for HTTP route")
		}
		r.Domain = normalizedDomain
		if r.PublicPort != 0 {
			return errors.New("public port is only valid for TCP route")
		}
	} else {
		r.Domain = NormalizeHost(r.Domain)
		if r.Domain != "" {
			return errors.New("domain is only valid for HTTP route")
		}
		if r.PublicPort < 0 || r.PublicPort > 65535 {
			return errors.New("public port must be between 1 and 65535 when set for TCP route")
		}
	}
	if !ValidHost(r.LocalHost) {
		return errors.New("invalid local host")
	}
	if r.LocalPort < 1 || r.LocalPort > 65535 {
		return errors.New("local port must be between 1 and 65535")
	}
	if r.TunnelGroup == "" {
		r.TunnelGroup = "web"
	}
	return nil
}
