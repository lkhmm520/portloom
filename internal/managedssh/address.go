package managedssh

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

const LegacyBindAddress = "127.0.0.1"

// BindAddress deterministically assigns an enrolled Agent an address in 127/8.
// The second octet intentionally excludes zero so isolated addresses never
// overlap the legacy shared 127.0.0.1 binding.
func BindAddress(agentID string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", errors.New("agent ID is required")
	}
	digest := sha256.Sum256([]byte(agentID))
	return fmt.Sprintf("127.%d.%d.%d", 1+int(digest[0])%254, digest[1], digest[2]), nil
}
