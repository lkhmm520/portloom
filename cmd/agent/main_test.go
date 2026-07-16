package main

import (
	"context"
	"testing"
)

func TestRunRejectsInvalidConfigurationBeforeStartingAgent(t *testing.T) {
	err := run(context.Background(), func(string) string { return "" })
	if err == nil {
		t.Fatal("expected configuration error")
	}
}
