package main

import (
	"strings"
	"testing"
)

func TestLoadServingNodeID(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		invalid  bool
	}{
		{"unset", "", false},
		{"configured", "node-west", false},
		{"case preserved", "Node-West", false},
		{"space padded", " node-west", true},
		{"list", "one,two", true},
		{"newline", "one\r\ntwo", true},
		{"control byte", "one\x7ftwo", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loadServingNodeID(func(key string) string {
				if key != "TM_SERVING_NODE_ID" {
					t.Fatalf("unexpected environment key %q", key)
				}
				return tc.id
			})
			if tc.invalid {
				if got != "" || err == nil || !strings.Contains(err.Error(), "TM_SERVING_NODE_ID") {
					t.Fatalf("got (%q, %v), want rejected identity", got, err)
				}
			} else if got != tc.id || err != nil {
				t.Fatalf("got (%q, %v), want (%q, nil)", got, err, tc.id)
			}
		})
	}
}
