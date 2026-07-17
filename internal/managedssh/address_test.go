package managedssh

import (
	"net"
	"testing"
)

func TestBindAddressIsStableDistinctLoopback(t *testing.T) {
	first, err := BindAddress("11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	again, err := BindAddress("11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	second, err := BindAddress("22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	if first != again || first == second {
		t.Fatalf("addresses first=%q again=%q second=%q", first, again, second)
	}
	if ip := net.ParseIP(first); ip == nil || !ip.IsLoopback() || first == LegacyBindAddress {
		t.Fatalf("isolated address = %q", first)
	}
}

func TestBindAddressRejectsEmptyAgentID(t *testing.T) {
	if _, err := BindAddress("  "); err == nil {
		t.Fatal("empty agent ID accepted")
	}
}
