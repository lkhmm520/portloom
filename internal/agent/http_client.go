package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxResponseBytes int64 = 4 << 20

type HTTPServerClient struct {
	baseURL    *url.URL
	clientID   string
	token      string
	httpClient *http.Client
}

func NewHTTPServerClient(rawURL, clientID, token string, allowInsecureHTTP bool, httpClient *http.Client) (*HTTPServerClient, error) {
	parsed, err := parseServerURL(rawURL, allowInsecureHTTP)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("client ID and token are required")
	}
	httpClient = httpClientWithServerURLPolicy(httpClient, allowInsecureHTTP)
	return &HTTPServerClient{baseURL: parsed, clientID: strings.TrimSpace(clientID), token: strings.TrimSpace(token), httpClient: httpClient}, nil
}
func (c *HTTPServerClient) FetchDesired(ctx context.Context, observedRevision int64) (DesiredState, error) {
	endpoint := c.endpoint("/api/v1/agent/desired")
	query := endpoint.Query()
	query.Set("observed_revision", strconv.FormatInt(observedRevision, 10))
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return DesiredState{}, fmt.Errorf("create desired request: %w", err)
	}
	c.authenticate(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DesiredState{}, fmt.Errorf("fetch desired state: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return DesiredState{}, err
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return DesiredState{}, fmt.Errorf("read desired state: %w", err)
	}
	var state DesiredState
	if err := json.Unmarshal(body, &state); err != nil {
		return DesiredState{}, fmt.Errorf("decode desired state: %w", err)
	}
	if state.Revision < 0 {
		return DesiredState{}, errors.New("desired revision must not be negative")
	}
	return state, nil
}
func (c *HTTPServerClient) ReportObserved(ctx context.Context, state ObservedState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode observed state: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/api/v1/agent/observed").String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create observed request: %w", err)
	}
	c.authenticate(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("report observed state: %w", err)
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}
func (c *HTTPServerClient) endpoint(path string) *url.URL {
	copy := *c.baseURL
	copy.Path = strings.TrimRight(copy.Path, "/") + path
	copy.RawQuery = ""
	copy.Fragment = ""
	return &copy
}
func (c *HTTPServerClient) authenticate(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Client-ID", c.clientID)
	req.Header.Set("Accept", "application/json")
}
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
}
func readLimited(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, errors.New("response exceeds size limit")
	}
	return body, nil
}
