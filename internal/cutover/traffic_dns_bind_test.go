package cutover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// PORTLOOM_TEST_BIND=1 go test ./internal/cutover -run TestRFC2136Bind -v
// Starts an isolated BIND with only a random loopback TCP port published.
func TestRFC2136Bind(t *testing.T) {
	if os.Getenv("PORTLOOM_TEST_BIND") != "1" {
		t.Skip("set PORTLOOM_TEST_BIND=1 to run disposable BIND via local Docker")
	}
	cfg := dnsTestConfig("127.0.0.1:53")
	cfg.Timeout = 5 * time.Second
	seed, err := NewRFC2136Traffic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := seed.MarkerValue(7)
	if err != nil {
		t.Fatal(err)
	}
	if marker != "portloom-v1:a15e1fdad7b3d2bd3bc6a27f8c867fad0593330aee54ced1559d1b8c540fae11:7" {
		t.Fatalf("unexpected provisioning marker: %s", marker)
	}
	const bindImage = "internetsystemsconsortium/bind9:9.20@sha256:978201d91548ec63c3b1c1befd5e6a3c9fa127f79f9058fd82d8e3493fe90ce1"
	conf := fmt.Sprintf(`options { directory "/tmp"; listen-on port 5353 { any; }; listen-on-v6 { none; }; recursion no; dnssec-validation no; notify no; };
controls { };
key "test-key.zone.test." { algorithm hmac-sha256; secret %q; };
zone "zone.test." { type primary; file "zone.db"; update-policy {
 grant test-key.zone.test. name a.zone.test. A AAAA;
 grant test-key.zone.test. name b.zone.test. A AAAA;
 grant test-key.zone.test. name epoch.zone.test. TXT;
}; };`, dnsTestSecret)
	zone := fmt.Sprintf(`$ORIGIN zone.test.
$TTL 180
@ IN SOA ns.zone.test. hostmaster.zone.test. 1 60 60 600 30
@ IN NS ns.zone.test.
ns IN A 192.0.2.53
a IN A 192.0.2.10
a IN A 192.0.2.11
a IN TXT "keep"
b IN A 192.0.2.10
b IN A 192.0.2.11
epoch IN TXT %q
other IN A 192.0.2.99
`, marker)
	id := strings.TrimSpace(bindDocker(t, "run", "--rm", "-d", "--user", "bind", "-p", "127.0.0.1::5353/tcp", "--entrypoint", "/bin/sh", bindImage, "-ec", `umask 077; printf '%s\n' "$1" >/tmp/test.conf; printf '%s\n' "$2" >/tmp/zone.db; exec /usr/sbin/named -g -n 1 -c /tmp/test.conf`, "sh", conf, zone))
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(bindDockerRun("logs", id))
		}
		if out, err := bindDockerRun("rm", "-f", id); err != nil {
			t.Errorf("clean up BIND: %v: %s", err, out)
		}
	})
	cfg.Primary = strings.TrimSpace(bindDocker(t, "port", id, "5353/tcp"))
	backend, err := NewRFC2136Traffic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Wait for the exact zone to load before asserting the baseline.
	for {
		_, err = backend.query(ctx, backend.zone, dns.TypeSOA)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("BIND did not become ready: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	check := func(g int64, want ...string) TrafficState {
		t.Helper()
		s, err := backend.InspectTraffic(ctx, cfg.Hosts)
		if err != nil || s.Generation != g || !slices.Equal(s.Addresses, bindAddrs(want...)) {
			t.Fatalf("inspect: got %+v, %v; want %d %v", s, err, g, want)
		}
		return s
	}
	old := check(7, "192.0.2.10", "192.0.2.11")
	soa, err := backend.query(ctx, backend.zone, dns.TypeSOA)
	if err != nil || soa[0].(*dns.SOA).Serial != 1 {
		t.Fatalf("prerequisite-only inspect changed the zone: %v %v", soa, err)
	}
	next := bindAddrs("2001:db8::10")
	// Make the last owner divergent without touching the generation. The CAS
	// must reject even though the first owner and marker still match.
	drift := new(dns.Msg).SetUpdate(backend.zone)
	extra := dnsAddressRRs("b.zone.test.", 180, bindAddrs("192.0.2.12"))
	drift.Insert(extra)
	if err := backend.update(ctx, drift); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.InspectTraffic(ctx, cfg.Hosts); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("divergent inspect: %v", err)
	}
	if err := backend.CompareTraffic(ctx, cfg.Hosts, old, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("divergent compare: %v", err)
	}
	rrs, err := backend.query(ctx, "a.zone.test.", dns.TypeA)
	if err != nil || len(rrs) != 2 {
		t.Fatalf("failed transaction changed first owner: %v %v", rrs, err)
	}
	txt, err := backend.query(ctx, backend.marker, dns.TypeTXT)
	if err != nil || len(txt) != 1 || txt[0].(*dns.TXT).Txt[0] != marker {
		t.Fatalf("failed transaction changed marker: %v %v", txt, err)
	}
	undo := new(dns.Msg).SetUpdate(backend.zone)
	undo.Remove(extra)
	if err := backend.update(ctx, undo); err != nil {
		t.Fatal(err)
	}
	check(7, "192.0.2.10", "192.0.2.11")
	// A family expected to be absent must also be protected from deletion.
	foreign6 := dnsAddressRRs("b.zone.test.", 180, bindAddrs("2001:db8::99"))
	drift = new(dns.Msg).SetUpdate(backend.zone)
	drift.Insert(foreign6)
	if err := backend.update(ctx, drift); err != nil {
		t.Fatal(err)
	}
	if err := backend.CompareTraffic(ctx, cfg.Hosts, old, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("unexpected AAAA compare: %v", err)
	}
	rrs, err = backend.query(ctx, "b.zone.test.", dns.TypeAAAA)
	if err != nil || len(rrs) != 1 || rrs[0].(*dns.AAAA).AAAA.String() != "2001:db8::99" {
		t.Fatalf("failed transaction erased unexpected AAAA: %v %v", rrs, err)
	}
	undo = new(dns.Msg).SetUpdate(backend.zone)
	undo.Remove(foreign6)
	if err := backend.update(ctx, undo); err != nil {
		t.Fatal(err)
	}

	// Two writers with the same expectations produce one winner at the real
	// authority. The losing transaction and a subsequent replay cannot advance
	// the marker or revert either member.
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() { <-start; results <- backend.CompareTraffic(ctx, cfg.Hosts, old, next) }()
	}
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			wins++
		case errors.Is(err, ErrSwitchConflict):
			conflicts++
		default:
			t.Fatalf("compare: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	changed := check(8, "2001:db8::10")
	if err := backend.CompareTraffic(ctx, cfg.Hosts, old, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("replay: %v", err)
	}
	check(8, "2001:db8::10")
	if err := backend.CompareTraffic(ctx, cfg.Hosts, changed, old.Addresses); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	rolledBack := check(9, "192.0.2.10", "192.0.2.11")
	rrs, err = backend.query(ctx, "other.zone.test.", dns.TypeA)
	if err != nil || len(rrs) != 1 || rrs[0].(*dns.A).A.String() != "192.0.2.99" || rrs[0].Header().Ttl != 180 {
		t.Fatalf("unrelated A changed: %v %v", rrs, err)
	}
	rrs, err = backend.query(ctx, "a.zone.test.", dns.TypeTXT)
	if err != nil || len(rrs) != 1 || !slices.Equal(rrs[0].(*dns.TXT).Txt, []string{"keep"}) {
		t.Fatalf("unrelated TXT changed: %v %v", rrs, err)
	}

	// Drop the ACK only after the real primary has processed the UPDATE.
	forward := bindDropReply(t, cfg.Primary)
	cfg.Primary = forward
	throughFault, err := NewRFC2136Traffic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := throughFault.CompareTraffic(ctx, cfg.Hosts, rolledBack, next); !errors.Is(err, ErrSwitchUnsettled) {
		t.Fatalf("lost ACK: %v", err)
	}
	check(10, "2001:db8::10")
	if err := backend.CompareTraffic(ctx, cfg.Hosts, rolledBack, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("replay after lost ACK: %v", err)
	}
	check(10, "2001:db8::10")
}

func bindDropReply(t *testing.T, primary string) string {
	t.Helper()
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_ = l.SetDeadline(time.Now().Add(5 * time.Second))
		down, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer down.Close()
		up, err := net.DialTimeout("tcp", primary, 5*time.Second)
		if err != nil {
			done <- err
			return
		}
		defer up.Close()
		_ = down.SetDeadline(time.Now().Add(5 * time.Second))
		_ = up.SetDeadline(time.Now().Add(5 * time.Second))
		d, u := &dns.Conn{Conn: down}, &dns.Conn{Conn: up}
		buf := make([]byte, dns.MaxMsgSize)
		n, err := d.Read(buf)
		if err == nil {
			_, err = u.Write(buf[:n])
		}
		if err == nil {
			_, err = u.Read(buf)
		}
		done <- err
	}()
	t.Cleanup(func() {
		_ = l.Close()
		if err := <-done; err != nil {
			t.Errorf("drop BIND reply: %v", err)
		}
	})
	return l.Addr().String()
}

func bindAddrs(raw ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(raw))
	for _, s := range raw {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func bindDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := bindDockerRun(args...)
	if err != nil {
		t.Fatalf("docker %s: %v: %s", args[0], err, out)
	}
	return out
}

func bindDockerRun(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}
