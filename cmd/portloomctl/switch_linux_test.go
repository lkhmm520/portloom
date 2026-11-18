package main

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestSwitchCLIStatusAndOptIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := dir + "/switch.json"
	if err := os.WriteFile(cfg, []byte(`{"enabled":false,"operation_id":"move-1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args       []string
		code       int
		diagnostic string
	}{
		{[]string{"status", "--journal-dir", dir, "--operation", "move-1"}, 1, "operation not found"},
		{[]string{"check", "--config", cfg, "--operation", "move-1"}, 2, "enabled"},
		{[]string{"switch", "--config", cfg, "--operation", "move-1"}, 2, "confirmation"},
		{[]string{"settle", "--config", cfg, "--operation", "move-1"}, 2, "confirmation"},
		{[]string{"rollback", "--config", cfg, "--operation", "move-1"}, 2, "confirmation"},
	} {
		var out, stderr bytes.Buffer
		if got := run(context.Background(), tt.args, &out, &stderr); got != tt.code || !bytes.Contains(stderr.Bytes(), []byte(tt.diagnostic)) || out.Len() != 0 {
			t.Errorf("%s: exit=%d stdout=%s stderr=%s", tt.args[0], got, &out, &stderr)
		}
	}
}
