package api

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func apiEd25519Key(fill byte) string {
	algorithm := []byte("ssh-ed25519")
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	wire := make([]byte, 4+len(algorithm)+4+len(key))
	binary.BigEndian.PutUint32(wire[:4], uint32(len(algorithm)))
	copy(wire[4:], algorithm)
	offset := 4 + len(algorithm)
	binary.BigEndian.PutUint32(wire[offset:offset+4], uint32(len(key)))
	copy(wire[offset+4:], key)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire) + " agent-comment"
}

func TestAgentCanRegisterRestrictedSSHKey(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateEnrollmentToken(context.Background(), "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.ConsumeEnrollmentToken(context.Background(), "enroll", "nas-one")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ssh", "authorized_keys")
	handler := New(s, Config{AdminToken: "admin", AuthorizedKeysPath: path})

	unauthorized := performJSON(t, handler, http.MethodPut, "/api/v1/agent/ssh-key", map[string]any{"public_key": apiEd25519Key(1)}, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	invalid := performJSON(t, handler, http.MethodPut, "/api/v1/agent/ssh-key", map[string]any{"public_key": "command=evil " + apiEd25519Key(1)}, token)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d body=%s", invalid.Code, invalid.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid request created authorized_keys: %v", err)
	}

	registered := performJSON(t, handler, http.MethodPut, "/api/v1/agent/ssh-key", map[string]any{"public_key": apiEd25519Key(1)}, token)
	if registered.Code != http.StatusNoContent {
		t.Fatalf("register status = %d body=%s", registered.Code, registered.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.Contains(line, `permitlisten="127.0.0.1:*"`) || !strings.Contains(line, "portloom-agent:") || strings.Contains(line, "agent-comment") {
		t.Fatalf("unsafe or incomplete authorized key line: %q", line)
	}

	repeated := performJSON(t, handler, http.MethodPut, "/api/v1/agent/ssh-key", map[string]any{"public_key": apiEd25519Key(1)}, token)
	if repeated.Code != http.StatusNoContent {
		t.Fatalf("idempotent register status = %d body=%s", repeated.Code, repeated.Body.String())
	}
	replaced := performJSON(t, handler, http.MethodPut, "/api/v1/agent/ssh-key", map[string]any{"public_key": apiEd25519Key(2)}, token)
	if replaced.Code != http.StatusConflict {
		t.Fatalf("replacement status = %d body=%s", replaced.Code, replaced.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != line {
		t.Fatalf("replacement changed authorized key: before=%q after=%q", line, after)
	}
}

func TestConcurrentAgentSSHKeyRegistrationsDoNotLoseEntries(t *testing.T) {
	const agents = 24
	s := openTestStore(t)
	path := filepath.Join(t.TempDir(), "authorized_keys")
	handler := New(s, Config{AdminToken: "admin", AuthorizedKeysPath: path, ManagedSSHIsolated: true})
	tokens := make([]string, agents)
	for index := range agents {
		secret := fmt.Sprintf("enroll-%d", index)
		if err := s.CreateEnrollmentToken(context.Background(), secret, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		_, token, err := s.ConsumeEnrollmentToken(context.Background(), secret, fmt.Sprintf("nas-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		tokens[index] = token
	}
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, agents)
	var group sync.WaitGroup
	for index, token := range tokens {
		group.Add(1)
		go func(index int, token string) {
			defer group.Done()
			<-start
			results <- performJSON(t, handler, http.MethodPut, "/api/v1/agent/ssh-key", map[string]any{"public_key": apiEd25519Key(byte(index + 1))}, token)
		}(index, token)
	}
	close(start)
	group.Wait()
	close(results)
	for response := range results {
		if response.Code != http.StatusNoContent {
			t.Fatalf("register status=%d body=%s", response.Code, response.Body.String())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(data)), "\n")); got != agents {
		t.Fatalf("authorized key entries=%d want %d\n%s", got, agents, data)
	}
}

func TestRegisteringSecondAgentPreservesFirstKey(t *testing.T) {
	s := openTestStore(t)
	path := filepath.Join(t.TempDir(), "authorized_keys")
	handler := New(s, Config{AdminToken: "admin", AuthorizedKeysPath: path})
	for index, name := range []string{"nas-one", "nas-two"} {
		secret := "enroll-" + name
		if err := s.CreateEnrollmentToken(context.Background(), secret, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		_, token, err := s.ConsumeEnrollmentToken(context.Background(), secret, name)
		if err != nil {
			t.Fatal(err)
		}
		response := performJSON(t, handler, http.MethodPut, "/api/v1/agent/ssh-key", map[string]any{"public_key": apiEd25519Key(byte(index + 1))}, token)
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d body=%s", name, response.Code, response.Body.String())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 2 {
		t.Fatalf("line count = %d, body=%q", lines, data)
	}
}
