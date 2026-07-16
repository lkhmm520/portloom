package agent

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func parseServerURL(rawURL string, allowInsecureHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid server URL")
	}
	if parsed.User != nil {
		return nil, errors.New("server URL must not contain credentials")
	}
	switch parsed.Scheme {
	case "https":
		return parsed, nil
	case "http":
		if !allowInsecureHTTP {
			return nil, errors.New("server URL must use HTTPS")
		}
		if !isLoopbackServerHost(parsed.Hostname()) {
			return nil, errors.New("insecure HTTP is allowed only for loopback server URLs")
		}
		return parsed, nil
	default:
		return nil, errors.New("server URL must use HTTPS")
	}
}

func isLoopbackServerHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func httpClientWithServerURLPolicy(client *http.Client, _ bool) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	secured := *client
	secured.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &secured
}
