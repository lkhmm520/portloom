package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Credentials struct {
	ClientID string `json:"client_id"`
	Token    string `json:"token"`
}

func Enroll(ctx context.Context, serverURL, name, enrollmentToken string, allowInsecureHTTP bool, client *http.Client) (Credentials, error) {
	requestID, err := randomCredentialValue()
	if err != nil {
		return Credentials{}, err
	}
	agentToken, err := randomCredentialValue()
	if err != nil {
		return Credentials{}, err
	}
	return enrollWithClaim(ctx, serverURL, name, enrollmentToken, requestID, agentToken, allowInsecureHTTP, client)
}

func enrollWithClaim(ctx context.Context, serverURL, name, enrollmentToken, requestID, agentToken string, allowInsecureHTTP bool, client *http.Client) (Credentials, error) {
	var credentials Credentials
	base, err := parseServerURL(serverURL, allowInsecureHTTP)
	if err != nil {
		return credentials, err
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(enrollmentToken) == "" {
		return credentials, errors.New("client name and enrollment token are required")
	}
	if len(requestID) != 64 || len(agentToken) != 64 {
		return credentials, errors.New("invalid enrollment claim")
	}
	payload, _ := json.Marshal(map[string]string{
		"name": strings.TrimSpace(name), "token": strings.TrimSpace(enrollmentToken),
		"request_id": requestID, "agent_token": agentToken,
	})
	endpoint := *base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/agent/enroll"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return credentials, fmt.Errorf("create enrollment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client = httpClientWithServerURLPolicy(client, allowInsecureHTTP)
	resp, err := client.Do(req)
	if err != nil {
		return credentials, fmt.Errorf("enroll agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return credentials, fmt.Errorf("enrollment failed with %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var response struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	if err := decoder.Decode(&response); err != nil {
		return credentials, fmt.Errorf("decode enrollment response: %w", err)
	}
	credentials = Credentials{ClientID: strings.TrimSpace(response.Agent.ID), Token: agentToken}
	if err := credentials.Validate(); err != nil {
		return Credentials{}, fmt.Errorf("invalid enrollment response: %w", err)
	}
	return credentials, nil
}

func (c Credentials) Validate() error {
	if strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.Token) == "" {
		return errors.New("client ID and token are required")
	}
	return nil
}

func LoadCredentials(path string) (Credentials, error) {
	var credentials Credentials
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return credentials, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil {
		return Credentials{}, fmt.Errorf("decode credentials: %w", err)
	}
	if err := credentials.Validate(); err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

func SaveCredentials(path string, credentials Credentials) error {
	if err := credentials.Validate(); err != nil {
		return err
	}
	path = filepath.Clean(path)
	if path == "." || !filepath.IsAbs(path) {
		return errors.New("credential path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	temp, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create credential file: %w", err)
	}
	cleanup := func() { _ = temp.Close(); _ = os.Remove(path + ".tmp") }
	if err := json.NewEncoder(temp).Encode(credentials); err != nil {
		cleanup()
		return fmt.Errorf("encode credentials: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync credentials: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(path + ".tmp")
		return fmt.Errorf("close credentials: %w", err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return fmt.Errorf("install credentials: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect credentials: %w", err)
	}
	return nil
}

func ResolveCredentials(ctx context.Context, cfg Config, client *http.Client) (Credentials, error) {
	if cfg.ClientID != "" || cfg.Token != "" {
		credentials := Credentials{ClientID: cfg.ClientID, Token: cfg.Token}
		return credentials, credentials.Validate()
	}
	pendingPath := cfg.StatePath + ".pending"
	if cfg.StatePath != "" {
		credentials, err := LoadCredentials(cfg.StatePath)
		if err == nil {
			_ = os.Remove(pendingPath)
			return credentials, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Credentials{}, fmt.Errorf("load saved credentials: %w", err)
		}
	}
	pending, err := LoadCredentials(pendingPath)
	if errors.Is(err, os.ErrNotExist) {
		requestID, genErr := randomCredentialValue()
		if genErr != nil {
			return Credentials{}, genErr
		}
		agentToken, genErr := randomCredentialValue()
		if genErr != nil {
			return Credentials{}, genErr
		}
		pending = Credentials{ClientID: requestID, Token: agentToken}
		if err := SaveCredentials(pendingPath, pending); err != nil {
			return Credentials{}, fmt.Errorf("save pending enrollment claim: %w", err)
		}
	} else if err != nil {
		return Credentials{}, fmt.Errorf("load pending enrollment claim: %w", err)
	}
	credentials, err := enrollWithClaim(ctx, cfg.ServerURL, cfg.ClientName, cfg.EnrollmentToken, pending.ClientID, pending.Token, cfg.AllowInsecureHTTP, client)
	if err != nil {
		return Credentials{}, err
	}
	if err := SaveCredentials(cfg.StatePath, credentials); err != nil {
		return Credentials{}, err
	}
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Credentials{}, fmt.Errorf("remove pending enrollment claim: %w", err)
	}
	return credentials, nil
}

func randomCredentialValue() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate enrollment claim: %w", err)
	}
	return hex.EncodeToString(value), nil
}
