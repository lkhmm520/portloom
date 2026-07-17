package authorizedkeys

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lkhmm520/portloom/internal/managedssh"
)

const baseRestrictions = `no-agent-forwarding,no-X11-forwarding,no-pty,no-user-rc`

type writeConfig struct {
	isolatedBindings bool
}

type WriteOption func(*writeConfig)

func WithIsolatedBindings() WriteOption {
	return func(config *writeConfig) { config.isolatedBindings = true }
}

var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Entry struct {
	AgentID   string
	PublicKey string
}

func Normalize(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("public key must be one line")
	}
	fields := strings.Fields(raw)
	if len(fields) < 2 || fields[0] != "ssh-ed25519" {
		return "", errors.New("an ssh-ed25519 public key is required")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", errors.New("invalid public key encoding")
	}
	algorithm, rest, ok := readString(blob)
	if !ok || string(algorithm) != "ssh-ed25519" {
		return "", errors.New("invalid public key algorithm")
	}
	key, rest, ok := readString(rest)
	if !ok || len(key) != 32 || len(rest) != 0 {
		return "", errors.New("invalid ed25519 public key")
	}
	return "ssh-ed25519 " + fields[1], nil
}

func readString(value []byte) ([]byte, []byte, bool) {
	if len(value) < 4 {
		return nil, nil, false
	}
	size := int(binary.BigEndian.Uint32(value[:4]))
	if size < 0 || size > len(value)-4 {
		return nil, nil, false
	}
	return value[4 : 4+size], value[4+size:], true
}

func Write(path string, entries []Entry, options ...WriteOption) error {
	config := writeConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	prepared := append([]Entry(nil), entries...)
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].AgentID < prepared[j].AgentID })
	var lines strings.Builder
	bindOwners := make(map[string]string, len(prepared))
	for _, entry := range prepared {
		if !agentIDPattern.MatchString(entry.AgentID) {
			return fmt.Errorf("unsafe agent ID %q", entry.AgentID)
		}
		key, err := Normalize(entry.PublicKey)
		if err != nil {
			return fmt.Errorf("agent %s: %w", entry.AgentID, err)
		}
		bindAddress := managedssh.LegacyBindAddress
		if config.isolatedBindings {
			bindAddress, err = managedssh.BindAddress(entry.AgentID)
			if err != nil {
				return fmt.Errorf("agent %s bind address: %w", entry.AgentID, err)
			}
			if owner, exists := bindOwners[bindAddress]; exists {
				return fmt.Errorf("isolated bind address collision between agents %s and %s", owner, entry.AgentID)
			}
			bindOwners[bindAddress] = entry.AgentID
		}
		restrictions := fmt.Sprintf(`%s,permitlisten="%s:*"`, baseRestrictions, bindAddress)
		fmt.Fprintf(&lines, "%s %s portloom-agent:%s\n", restrictions, key, entry.AgentID)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create authorized keys directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".authorized_keys-*")
	if err != nil {
		return fmt.Errorf("create temporary authorized keys: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set authorized keys mode: %w", err)
	}
	if _, err := temporary.WriteString(lines.String()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write authorized keys: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync authorized keys: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close authorized keys: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace authorized keys: %w", err)
	}
	return nil
}
