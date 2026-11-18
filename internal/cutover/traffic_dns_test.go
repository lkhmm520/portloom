package cutover

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const dnsTestSecret = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=" // synthetic only

func dnsTestConfig(primary string) RFC2136Config {
	return RFC2136Config{Primary: primary, Zone: "zone.test.", Hosts: []string{"b.zone.test", "a.zone.test"}, Marker: "epoch.zone.test.", KeyName: "test-key.zone.test.", Secret: dnsTestSecret, TTL: 60, Timeout: time.Second}
}

func dnsTestServer(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	s := &dns.Server{Listener: l, Handler: handler, TsigSecret: map[string]string{"test-key.zone.test.": dnsTestSecret}, NotifyStartedFunc: func() { close(ready) }, MsgAcceptFunc: func(dns.Header) dns.MsgAcceptAction { return dns.MsgAccept }}
	done := make(chan error, 1)
	go func() { done <- s.ActivateAndServe() }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return l.Addr().String()
}

func dnsTestReply(w dns.ResponseWriter, req, resp *dns.Msg) {
	if ts := req.IsTsig(); ts != nil && w.TsigStatus() == nil {
		resp.SetTsig(ts.Hdr.Name, ts.Algorithm, 60, time.Now().Unix())
	}
	_ = w.WriteMsg(resp)
}

func TestCoordinatorRollbackAfterDNSUpdateRejection(t *testing.T) {
	for _, mode := range []string{"signed refusal", "unsigned refusal", "malformed refusal", "refusal without readback", "refusal without journal save", "lost reply then signed refusals"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newSwitchFixture()
			if mode == "refusal without journal save" {
				f.failSave = func(p SwitchProgress) bool {
					return p.Status == SwitchTargetPromoted && p.Pending == "" && f.saved.Pending == moveTraffic
				}
			}
			cfg := dnsTestConfig("127.0.0.1:53")
			cfg.Zone, cfg.Marker, cfg.Hosts = "example.test.", "epoch.example.test.", f.spec.Hosts
			seed, err := NewRFC2136Traffic(cfg)
			if err != nil {
				t.Fatal(err)
			}
			baseline := f.traffic
			var updates atomic.Int32
			var allowReadback atomic.Bool
			cfg.Primary = dnsTestServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
				resp := new(dns.Msg).SetReply(req)
				if req.Opcode == dns.OpcodeUpdate {
					if len(req.Ns) != 0 {
						n := updates.Add(1)
						resp.Rcode = dns.RcodeRefused
						switch {
						case mode == "lost reply then signed refusals" && n == 1:
							_ = w.Close()
							return
						case mode == "unsigned refusal":
							_ = w.WriteMsg(resp)
							return
						case mode == "malformed refusal":
							resp.Question[0].Name = "other.test."
						}
					} else if mode == "refusal without readback" && updates.Load() > 0 && !allowReadback.Load() {
						resp.Rcode = dns.RcodeRefused
					}
				} else {
					resp.Authoritative = true
					q := req.Question[0]
					soa := &dns.SOA{Hdr: dns.RR_Header{Name: cfg.Zone, Class: dns.ClassINET, Rrtype: dns.TypeSOA}, Ns: "ns.example.test.", Mbox: "hostmaster.example.test.", Serial: 1}
					switch q.Qtype {
					case dns.TypeSOA:
						resp.Answer = []dns.RR{soa}
					case dns.TypeTXT:
						resp.Answer = []dns.RR{seed.markerRR(baseline.Generation, 60)}
					case dns.TypeA:
						resp.Answer = dnsAddressRRs(q.Name, 60, baseline.Addresses)
					case dns.TypeAAAA:
						resp.Ns = []dns.RR{soa}
					}
				}
				dnsTestReply(w, req, resp)
			})
			traffic, err := NewRFC2136Traffic(cfg)
			if err != nil {
				t.Fatal(err)
			}
			coordinator := func() *Coordinator { return NewCoordinator(f, traffic, f, f) }
			p, err := coordinator().Start(ctx, f.spec)
			if err == nil || p.Status != SwitchTargetPromoted || updates.Load() != 1 {
				t.Fatalf("start = %s/%s, %v; updates = %d", p.Status, p.Pending, err, updates.Load())
			}
			f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen")
			f.events = nil
			if mode == "signed refusal" {
				if !errors.Is(err, ErrSwitchWriteRejected) || errors.Is(err, ErrSwitchUnsettled) || p.Pending != "" || f.saved.Pending != "" {
					t.Fatalf("final refusal with exact pre-state left pending = %q, %v", p.Pending, err)
				}
				p, err = coordinator().Rollback(ctx, f.spec.OperationID)
				if err != nil || p.Status != SwitchRolledBack || p.Pending != "" || updates.Load() != 1 ||
					p.Traffic.Generation != baseline.Generation || !slices.Equal(p.Traffic.Addresses, baseline.Addresses) {
					t.Fatalf("rollback = %s/%s traffic=%+v, %v; updates = %d", p.Status, p.Pending, p.Traffic, err, updates.Load())
				}
				f.want(t, "target:blocked", "source:frozen", "public:source")
				return
			}
			if !errors.Is(err, ErrSwitchUnsettled) || p.Pending != moveTraffic {
				t.Fatalf("unknown outcome lost pending = %q, %v", p.Pending, err)
			}
			allowReadback.Store(true)
			wantUpdates := int32(1)
			if mode == "lost reply then signed refusals" {
				// A refusal of either replay cannot rule out the first UPDATE
				// still being in flight, regardless of exact pre-state readback.
				for _, retry := range []func(context.Context, string) (SwitchProgress, error){coordinator().SettlePending, coordinator().Forward} {
					p, err = retry(ctx, f.spec.OperationID)
					wantUpdates++
					if !errors.Is(err, ErrSwitchUnsettled) || p.Pending != moveTraffic || updates.Load() != wantUpdates {
						t.Fatalf("replay = %s/%s, %v; updates = %d", p.Status, p.Pending, err, updates.Load())
					}
				}
			}
			p, err = coordinator().Rollback(ctx, f.spec.OperationID)
			if !errors.Is(err, ErrSwitchUnsettled) || p.Status != SwitchTargetPromoted || p.Pending != moveTraffic || f.saved.Pending != moveTraffic || updates.Load() != wantUpdates {
				t.Fatalf("ambiguous rollback = %s/%s, %v; updates = %d", p.Status, p.Pending, err, updates.Load())
			}
			f.want(t) // no target block, source restore, or public probe
		})
	}
}

func TestRFC2136CompareRejectsStaleInSingleSignedUpdate(t *testing.T) {
	requests := make(chan *dns.Msg, 8)
	primary := dnsTestServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
		if req.IsTsig() == nil || w.TsigStatus() != nil || req.RecursionDesired {
			t.Error("expected nonrecursive signed request")
		}
		requests <- req
		resp := new(dns.Msg).SetRcode(req, dns.RcodeNXRrset)
		// RFC permits echoed UPDATE sections. Encode empty typed RR headers
		// explicitly so repacking them doesn't insert type-specific zero RDATA.
		echo := func(rrs []dns.RR) []dns.RR {
			out := slices.Clone(rrs)
			for i, rr := range out {
				if rr.Header().Rdlength == 0 {
					out[i] = &dns.ANY{Hdr: *rr.Header()}
				}
			}
			return out
		}
		resp.Answer, resp.Ns = echo(req.Answer), echo(req.Ns)
		dnsTestReply(w, req, resp)
	})
	cfg := dnsTestConfig(primary)
	backend, err := NewRFC2136Traffic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := TrafficState{Generation: 7, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.22"), netip.MustParseAddr("192.0.2.11")}}
	next := []netip.Addr{netip.MustParseAddr("2001:db8::1")}
	if err := backend.CompareTraffic(context.Background(), cfg.Hosts, old, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	u := <-requests
	if u.Opcode != dns.OpcodeUpdate || len(u.Question) != 1 || u.Question[0] != (dns.Question{Name: "zone.test.", Qtype: dns.TypeSOA, Qclass: dns.ClassINET}) {
		t.Fatalf("bad UPDATE zone: %v", u.Question)
	}
	prereqs := map[string]int{}
	for _, rr := range u.Answer {
		h := rr.Header()
		if h.Ttl != 0 {
			t.Fatalf("bad prerequisite TTL: %v", rr)
		}
		prereqs[rr.String()]++
	}
	for _, h := range []string{"a.zone.test.", "b.zone.test."} {
		for _, want := range []string{h + "\t0\tIN\tA\t192.0.2.11", h + "\t0\tIN\tA\t192.0.2.22", h + "\t0\tNONE\tAAAA\t", h + "\t0\tNONE\tCNAME\t"} {
			if prereqs[want] != 1 {
				t.Errorf("missing %q in %v", want, prereqs)
			}
		}
	}
	if len(u.Ns) != 8 {
		t.Fatalf("bad update records: %v", u.Ns)
	}
	for _, rr := range u.Ns {
		h := rr.Header()
		if h.Name != "a.zone.test." && h.Name != "b.zone.test." && h.Name != "epoch.zone.test." || h.Rrtype == dns.TypeANY {
			t.Errorf("broad update: %v", rr)
		}
	}
}

func TestRFC2136UntrustedUpdateReplyIsUnsettled(t *testing.T) {
	for _, mode := range []string{"unsigned", "wrong opcode", "wrong question", "wrong algorithm", "closed", "readback failure"} {
		t.Run(mode, func(t *testing.T) {
			cfg := dnsTestConfig(dnsTestServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
				resp := new(dns.Msg).SetReply(req)
				switch mode {
				case "unsigned":
					_ = w.WriteMsg(resp)
				case "wrong opcode":
					resp.Opcode = dns.OpcodeQuery
					dnsTestReply(w, req, resp)
				case "wrong question":
					resp.Question[0].Name = "elsewhere.test."
					dnsTestReply(w, req, resp)
				case "wrong algorithm":
					resp.SetTsig(req.IsTsig().Hdr.Name, dns.HmacSHA512, 60, time.Now().Unix())
					_ = w.WriteMsg(resp)
				case "closed":
					_ = w.Close()
				case "readback failure":
					// Signed UPDATE success, but subsequent QUERY lacks AA.
					dnsTestReply(w, req, resp)
				}
			}))
			b, err := NewRFC2136Traffic(cfg)
			if err != nil {
				t.Fatal(err)
			}
			old := TrafficState{Generation: 7, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.11")}}
			err = b.CompareTraffic(context.Background(), cfg.Hosts, old, []netip.Addr{netip.MustParseAddr("192.0.2.12")})
			if !errors.Is(err, ErrSwitchUnsettled) {
				t.Fatalf("expected unsettled, got %v", err)
			}
		})
	}
}

func TestRFC2136RejectsInvalidScopeAndCompareBeforeIO(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*RFC2136Config)
	}{
		{"resolver name", func(c *RFC2136Config) { c.Primary = "ns.zone.test:53" }},
		{"wrong zone", func(c *RFC2136Config) { c.Zone = "other.test." }},
		{"suffix lookalike", func(c *RFC2136Config) { c.Hosts = []string{"notzone.test"} }},
		{"wildcard", func(c *RFC2136Config) { c.Hosts = []string{"*.zone.test"} }},
		{"duplicate", func(c *RFC2136Config) { c.Hosts = []string{"A.zone.test.", "a.zone.test"} }},
		{"overlap", func(c *RFC2136Config) { c.Marker = "a.zone.test" }},
		{"invalid key", func(c *RFC2136Config) { c.KeyName = "." }},
		{"short secret", func(c *RFC2136Config) { c.Secret = "dGVzdA==" }},
		{"zero TTL", func(c *RFC2136Config) { c.TTL = 0 }},
		{"oversized transaction", func(c *RFC2136Config) {
			c.Hosts = nil
			for i := range maxDNSHosts {
				c.Hosts = append(c.Hosts, fmt.Sprintf("%s%02d.zone.test", strings.Repeat("a", 60), i))
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := dnsTestConfig("127.0.0.1:53")
			test.edit(&cfg)
			if _, err := NewRFC2136Traffic(cfg); !errors.Is(err, ErrInput) {
				t.Fatalf("want invalid input, got %v", err)
			}
		})
	}
	cfg := dnsTestConfig(dnsTestServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
		t.Error("invalid scope/compare must not reach authority")
		_ = w.Close()
	}))
	b, err := NewRFC2136Traffic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	old := TrafficState{Generation: 7, Addresses: bindAddrs("192.0.2.11")}
	next := bindAddrs("192.0.2.12")
	if _, err := b.InspectTraffic(ctx, cfg.Hosts[:1]); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("partial group: %v", err)
	}
	if err := b.CompareTraffic(ctx, []string{"a.zone.test", "other.zone.test"}, old, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("different group: %v", err)
	}
	if err := b.CompareTraffic(ctx, cfg.Hosts, old, append(slices.Clone(next), next...)); !errors.Is(err, ErrInput) {
		t.Fatalf("duplicate addresses: %v", err)
	}
	old.Generation = math.MaxInt64
	if err := b.CompareTraffic(ctx, cfg.Hosts, old, next); !errors.Is(err, ErrInput) {
		t.Fatalf("generation overflow: %v", err)
	}
}

func TestRFC2136InspectRequiresCoherentExactAuthoritativeState(t *testing.T) {
	for _, test := range []struct {
		mode  string
		proof bool
	}{
		{"valid", true}, {"changed before proof", true}, {"HTTPS at exact owner", true},
		{"wrong group", false}, {"missing marker", false}, {"aliased answer", false},
		{"divergent hosts", false}, {"wrong negative SOA", false}, {"nonauthoritative", false},
	} {
		t.Run(test.mode, func(t *testing.T) {
			seed, err := NewRFC2136Traffic(dnsTestConfig("127.0.0.1:53"))
			if err != nil {
				t.Fatal(err)
			}
			proofs := make(chan struct{}, 2)
			cfg := dnsTestConfig(dnsTestServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
				resp := new(dns.Msg).SetReply(req)
				if req.Opcode == dns.OpcodeUpdate {
					proofs <- struct{}{}
					if len(req.Ns) != 0 {
						t.Error("inspect must not write")
					}
					if test.mode == "changed before proof" {
						resp.Rcode = dns.RcodeNXRrset
					}
					if test.mode == "HTTPS at exact owner" {
						if !slices.ContainsFunc(req.Answer, func(rr dns.RR) bool {
							h := rr.Header()
							return h.Name == "b.zone.test." && h.Class == dns.ClassNONE && h.Rrtype == dns.TypeHTTPS
						}) {
							t.Error("inspection omitted HTTPS prerequisite")
						}
						resp.Rcode = dns.RcodeYXRrset
					}
					dnsTestReply(w, req, resp)
					return
				}
				resp.Authoritative = test.mode != "nonauthoritative"
				q := req.Question[0]
				soa := &dns.SOA{Hdr: dns.RR_Header{Name: "zone.test.", Class: dns.ClassINET, Rrtype: dns.TypeSOA}, Ns: "ns.zone.test.", Mbox: "hostmaster.zone.test.", Serial: 1}
				switch q.Qtype {
				case dns.TypeSOA:
					resp.Answer = []dns.RR{soa}
				case dns.TypeTXT:
					marker := seed.markerRR(7, 60)
					if test.mode == "wrong group" {
						marker.Txt[0] = "portloom-v1:" + strings.Repeat("a", 64) + ":7"
					}
					if test.mode == "missing marker" {
						resp.Ns = []dns.RR{soa}
					} else {
						resp.Answer = []dns.RR{marker}
					}
				case dns.TypeA:
					addr := "192.0.2.10"
					if q.Name == "b.zone.test." && test.mode == "divergent hosts" {
						addr = "192.0.2.11"
					}
					resp.Answer = dnsAddressRRs(q.Name, 60, bindAddrs(addr))
					if test.mode == "aliased answer" {
						resp.Answer = []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Rrtype: dns.TypeCNAME}, Target: "other.zone.test."}}
					}
				case dns.TypeAAAA:
					if test.mode == "wrong negative SOA" {
						soa.Hdr.Name = "other.test."
					}
					resp.Ns = []dns.RR{soa}
				default:
					t.Errorf("unexpected query: %v", q)
				}
				dnsTestReply(w, req, resp)
			}))
			b, err := NewRFC2136Traffic(cfg)
			if err != nil {
				t.Fatal(err)
			}
			s, err := b.InspectTraffic(context.Background(), []string{"A.ZONE.TEST.", "b.zone.test"})
			if test.mode == "valid" {
				if err != nil || s.Generation != 7 || !slices.Equal(s.Addresses, bindAddrs("192.0.2.10")) {
					t.Fatalf("inspect: %+v, %v", s, err)
				}
			} else if !errors.Is(err, ErrSwitchConflict) || s.Generation != 0 || len(s.Addresses) != 0 {
				t.Fatalf("expected conflict with no snapshot, got %+v, %v", s, err)
			}
			if (len(proofs) == 1) != test.proof {
				t.Fatalf("unexpected number of atomic proofs: %d", len(proofs))
			}
		})
	}
}
