package authorizedkeys

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkhmm520/portloom/internal/managedssh"
)

func testEd25519Key(t *testing.T, fill byte) string {
	t.Helper()
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
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire) + " generated-comment"
}

func TestNormalizeAcceptsOnlyBareEd25519Keys(t *testing.T) {
	key := testEd25519Key(t, 7)
	normalized, err := Normalize(key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(normalized, "generated-comment") || !strings.HasPrefix(normalized, "ssh-ed25519 ") {
		t.Fatalf("unexpected normalized key %q", normalized)
	}
	invalid := []string{
		"command=evil " + key,
		key + "\nssh-ed25519 AAAA injected",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ==",
		"ssh-ed25519 not-base64",
	}
	for _, value := range invalid {
		if _, err := Normalize(value); err == nil {
			t.Fatalf("Normalize(%q) succeeded", value)
		}
	}
}

func TestWriteUsesRestrictedSortedEntriesAndMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh", "authorized_keys")
	entries := []Entry{
		{AgentID: "agent-b", PublicKey: testEd25519Key(t, 2)},
		{AgentID: "agent-a", PublicKey: testEd25519Key(t, 1)},
	}
	if err := Write(path, entries); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "portloom-agent:agent-a") || !strings.HasSuffix(lines[1], "portloom-agent:agent-b") {
		t.Fatalf("entries are not deterministic: %q", text)
	}
	for _, line := range lines {
		for _, required := range []string{"no-agent-forwarding", "no-X11-forwarding", "no-pty", "no-user-rc", `permitlisten="127.0.0.1:*"`} {
			if !strings.Contains(line, required) {
				t.Fatalf("line %q missing %q", line, required)
			}
		}
		if strings.Contains(line, "command=") {
			t.Fatalf("line unexpectedly forces a command: %q", line)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteRejectsUnsafeAgentIDWithoutReplacingExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(path, []byte("keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Write(path, []Entry{{AgentID: "bad\nkey", PublicKey: testEd25519Key(t, 3)}})
	if err == nil {
		t.Fatal("unsafe agent ID was accepted")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "keep-me\n" {
		t.Fatalf("existing file changed to %q", data)
	}
}

func TestWriteIsolatedRestrictsEachKeyToItsAgentAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	entries := []Entry{
		{AgentID: "agent-a", PublicKey: testEd25519Key(t, 1)},
		{AgentID: "agent-b", PublicKey: testEd25519Key(t, 2)},
	}
	if err := Write(path, entries, WithIsolatedBindings()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(entries) {
		t.Fatalf("lines=%q", data)
	}
	for index, entry := range entries {
		address, err := managedssh.BindAddress(entry.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(lines[index], `permitlisten="`+address+`:*"`) {
			t.Fatalf("line %q does not restrict %s to %s", lines[index], entry.AgentID, address)
		}
		if strings.Contains(lines[index], `permitlisten="127.0.0.1:*"`) {
			t.Fatalf("isolated line retained shared bind address: %q", lines[index])
		}
	}
}

func TestWriteRejectsIsolatedBindAddressCollision(t *testing.T) {
	key := testEd25519Key(t, 9)
	err := Write(filepath.Join(t.TempDir(), "authorized_keys"), []Entry{
		{AgentID: "same-agent", PublicKey: key},
		{AgentID: "same-agent", PublicKey: key},
	}, WithIsolatedBindings())
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("collision error = %v", err)
	}
}
