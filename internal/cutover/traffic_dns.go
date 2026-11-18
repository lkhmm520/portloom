package cutover

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const (
	maxDNSHosts      = 32
	maxDNSAddresses  = 16
	maxDNSGuardNames = 128
	maxDNSUpdateSize = dns.MaxMsgSize - 512 // leave room for TSIG and framing
)

// RFC2136Config binds one adapter to one exact group in one controlled IN zone.
// Primary must be the actual writable primary's numeric IP:port, not a resolver,
// forwarding secondary, anycast endpoint or load balancer. Use a restricted
// HMAC-SHA256 TSIG key authorized for only these hosts' A/AAAA and Marker TXT.
// Secret is the base64-encoded key (at least 32 random bytes). TTL is seconds
// for inserted RRsets; Timeout bounds a complete operation (zero means 10s).
// Do not share these RRsets with overlapping cutover groups or other writers;
// never reset the marker generation when restarting or restoring the authority.
// Use the same zone view as the public hosts, without DNS proxying or
// synthesized answers. DNSSEC signing and transfers belong to the authority.
// This adapter verifies the primary, not public resolver/secondary propagation.
// The zone must answer signed nonrecursive queries and accept both a
// prerequisite-only UPDATE and the actual address/marker UPDATE.
type RFC2136Config struct {
	Primary string
	Zone    string
	Hosts   []string
	Marker  string
	KeyName string
	Secret  string `json:"-"`
	TTL     uint32
	Timeout time.Duration
}

type RFC2136Traffic struct {
	primary, zone, marker, key, markerPrefix string
	hosts, guardNames                        []string
	ttl                                      uint32
	timeout                                  time.Duration
	client                                   dns.Client
}

var _ SwitchTraffic = (*RFC2136Traffic)(nil)

func NewRFC2136Traffic(cfg RFC2136Config) (*RFC2136Traffic, error) {
	endpoint, err := netip.ParseAddrPort(cfg.Primary)
	addr := endpoint.Addr().Unmap()
	if err != nil || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" || (!addr.IsGlobalUnicast() && !addr.IsLoopback()) {
		return nil, fmt.Errorf("%w: RFC 2136 primary must be a numeric unicast IP:port", ErrInput)
	}
	zone, err := dnsConfigName(cfg.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid RFC 2136 zone", ErrInput)
	}
	marker, err := dnsConfigName(cfg.Marker)
	if err != nil || marker == zone || !dns.IsSubDomain(zone, marker) {
		return nil, fmt.Errorf("%w: marker must be a dedicated owner below the zone apex", ErrInput)
	}
	key, err := dnsConfigName(cfg.KeyName)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid TSIG key name", ErrInput)
	}
	if len(cfg.Secret) > 1024 {
		return nil, fmt.Errorf("%w: invalid TSIG secret", ErrInput)
	}
	secret, err := base64.StdEncoding.Strict().DecodeString(cfg.Secret)
	if err != nil || len(secret) < 32 || base64.StdEncoding.EncodeToString(secret) != cfg.Secret {
		return nil, fmt.Errorf("%w: TSIG secret must be canonical base64 with at least 32 decoded bytes", ErrInput)
	}
	if cfg.TTL == 0 || cfg.TTL > math.MaxInt32 || cfg.Timeout < 0 || cfg.Timeout > time.Minute {
		return nil, fmt.Errorf("%w: invalid RFC 2136 TTL or timeout", ErrInput)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	hosts, err := dnsGroup(cfg.Hosts)
	if err != nil {
		return nil, err
	}
	guard := map[string]bool{zone: true}
	for _, owner := range append(slices.Clone(hosts), marker) {
		if !dns.IsSubDomain(zone, owner) {
			return nil, fmt.Errorf("%w: group owner outside RFC 2136 zone", ErrInput)
		}
		for name := owner; name != zone; name = name[strings.IndexByte(name, '.')+1:] {
			guard[name] = true
		}
	}
	if slices.Contains(hosts, marker) || len(guard) > maxDNSGuardNames {
		return nil, fmt.Errorf("%w: invalid RFC 2136 group or marker scope", ErrInput)
	}
	names := make([]string, 0, len(guard))
	for name := range guard {
		names = append(names, name)
	}
	slices.Sort(names)
	backend := &RFC2136Traffic{
		primary: endpoint.String(), zone: zone, marker: marker, key: key,
		markerPrefix: fmt.Sprintf("portloom-v1:%x:", sha256.Sum256([]byte(strings.Join(hosts, "\n")))),
		hosts:        hosts, guardNames: names, ttl: cfg.TTL, timeout: cfg.Timeout,
		client: dns.Client{Net: "tcp", Timeout: cfg.Timeout, TsigSecret: map[string]string{key: cfg.Secret}},
	}
	// Fail before the coordinator can move either node if this scope cannot
	// fit a single UPDATE for the largest admitted address set (all AAAA).
	worst := make([]netip.Addr, maxDNSAddresses)
	for i := range worst {
		worst[i] = netip.AddrFrom16([16]byte{0x20, 1, 0x0d, 0xb8, 15: byte(i)})
	}
	m := backend.replacement(TrafficState{Generation: math.MaxInt64 - 1, Addresses: worst}, worst)
	m.SetTsig(key, dns.HmacSHA256, 60, 0)
	if m.Len() > maxDNSUpdateSize {
		return nil, fmt.Errorf("%w: RFC 2136 group does not fit one UPDATE", ErrInput)
	}
	return backend, nil
}

func dnsConfigName(raw string) (string, error) {
	names, err := normalizeHosts([]string{raw})
	if err != nil {
		return "", err
	}
	return dns.Fqdn(names[0]), nil
}

func dnsGroup(raw []string) ([]string, error) {
	if len(raw) == 0 || len(raw) > maxDNSHosts {
		return nil, fmt.Errorf("%w: RFC 2136 requires 1 to %d exact hosts", ErrInput, maxDNSHosts)
	}
	hosts, err := normalizeHosts(raw)
	if err != nil {
		return nil, err
	}
	if len(hosts) != len(raw) {
		return nil, fmt.Errorf("%w: duplicate RFC 2136 host", ErrInput)
	}
	for i := range hosts {
		hosts[i] = dns.Fqdn(hosts[i])
	}
	slices.Sort(hosts)
	return hosts, nil
}

// MarkerValue returns the single TXT character-string to provision out of band
// at Marker. Provision only during initial setup, alongside the baseline RRsets.
// The group hash is SHA-256 of the sorted, lowercase, fully qualified hostnames
// (with trailing dots) joined by newlines, with no final newline.
func (b *RFC2136Traffic) MarkerValue(generation int64) (string, error) {
	if generation <= 0 {
		return "", fmt.Errorf("%w: DNS marker generation must be positive", ErrInput)
	}
	return b.markerPrefix + strconv.FormatInt(generation, 10), nil
}

func (b *RFC2136Traffic) checkScope(raw []string) error {
	hosts, err := dnsGroup(raw)
	if err != nil {
		return err
	}
	if !slices.Equal(b.hosts, hosts) {
		return fmt.Errorf("%w: RFC 2136 group differs from configured scope", ErrSwitchConflict)
	}
	return nil
}

func dnsAddresses(input []netip.Addr) ([]netip.Addr, error) {
	if len(input) > maxDNSAddresses {
		return nil, fmt.Errorf("%w: too many RFC 2136 addresses", ErrInput)
	}
	return normalizeAddresses(input)
}

func (b *RFC2136Traffic) InspectTraffic(ctx context.Context, hosts []string) (TrafficState, error) {
	if err := b.checkScope(hosts); err != nil {
		return TrafficState{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inspect(ctx)
}

func (b *RFC2136Traffic) inspect(ctx context.Context) (TrafficState, error) {
	var state TrafficState
	if _, err := b.query(ctx, b.zone, dns.TypeSOA); err != nil {
		return state, err
	}
	txt, err := b.query(ctx, b.marker, dns.TypeTXT)
	if err != nil {
		return state, err
	}
	if len(txt) != 1 {
		return state, fmt.Errorf("%w: expected one DNS generation TXT record", ErrSwitchConflict)
	}
	data, ok := txt[0].(*dns.TXT)
	if !ok || len(data.Txt) != 1 || !strings.HasPrefix(data.Txt[0], b.markerPrefix) {
		return state, fmt.Errorf("%w: DNS marker does not bind this exact group", ErrSwitchConflict)
	}
	gen, err := strconv.ParseInt(strings.TrimPrefix(data.Txt[0], b.markerPrefix), 10, 64)
	if err != nil || gen <= 0 || data.Txt[0] != b.markerPrefix+strconv.FormatInt(gen, 10) {
		return state, fmt.Errorf("%w: invalid DNS generation marker", ErrSwitchConflict)
	}
	state.Generation = gen
	for _, host := range b.hosts {
		var addresses []netip.Addr
		for _, typ := range []uint16{dns.TypeA, dns.TypeAAAA} {
			rrs, err := b.query(ctx, host, typ)
			if err != nil {
				return TrafficState{}, err
			}
			for _, rr := range rrs {
				var ip net.IP
				switch r := rr.(type) {
				case *dns.A:
					ip = r.A
				case *dns.AAAA:
					ip = r.AAAA
				}
				addr, ok := netip.AddrFromSlice(ip)
				if !ok || (typ == dns.TypeAAAA && (!addr.Is6() || addr.Is4In6())) {
					return TrafficState{}, fmt.Errorf("%w: invalid A/AAAA wire address", ErrSwitchConflict)
				}
				addresses = append(addresses, addr.Unmap())
			}
		}
		addresses, err = dnsAddresses(addresses)
		if err != nil {
			return TrafficState{}, fmt.Errorf("%w: invalid or missing addresses on %s", ErrSwitchConflict, host)
		}
		if state.Addresses == nil {
			state.Addresses = addresses
		} else if !slices.Equal(state.Addresses, addresses) {
			return TrafficState{}, fmt.Errorf("%w: divergent DNS address RRsets", ErrSwitchConflict)
		}
	}
	// Ordinary lookups are not a group snapshot. A prerequisite-only UPDATE
	// proves that the marker and all exact (non-synthesized) RRsets coexisted on
	// the writable zone, and that no unsupported routing was present at that point.
	if err := b.update(ctx, b.prerequisites(state)); err != nil {
		return TrafficState{}, err
	}
	return state, nil
}

// CompareTraffic sends one mutating UPDATE. After a confirmed response it
// requires exact primary readback. If the outcome is unsettled, inspect with
// a fresh context and compare against both journaled barriers; do not advance
// the expected generation and issue a new write based on the error alone.
func (b *RFC2136Traffic) CompareTraffic(ctx context.Context, hosts []string, expected TrafficState, next []netip.Addr) error {
	if err := b.checkScope(hosts); err != nil {
		return err
	}
	if expected.Generation <= 0 || expected.Generation == math.MaxInt64 {
		return fmt.Errorf("%w: invalid expected DNS generation", ErrInput)
	}
	old, err := dnsAddresses(expected.Addresses)
	if err != nil {
		return err
	}
	next, err = dnsAddresses(next)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	expected.Addresses = old
	if err := b.update(ctx, b.replacement(expected, next)); err != nil {
		return err
	}
	actual, err := b.inspect(ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("%w: DNS update readback failed", ErrSwitchUnsettled), err)
	}
	if actual.Generation != expected.Generation+1 || !slices.Equal(actual.Addresses, next) {
		return fmt.Errorf("%w: DNS update readback differs from intended generation and addresses", ErrSwitchUnsettled)
	}
	return nil
}

func (b *RFC2136Traffic) replacement(expected TrafficState, next []netip.Addr) *dns.Msg {
	m := b.prerequisites(expected)
	for _, host := range b.hosts {
		m.RemoveRRset([]dns.RR{dnsOwner(host, dns.TypeA), dnsOwner(host, dns.TypeAAAA)})
		m.Insert(dnsAddressRRs(host, b.ttl, next))
	}
	m.RemoveRRset([]dns.RR{dnsOwner(b.marker, dns.TypeTXT)})
	m.Insert([]dns.RR{b.markerRR(expected.Generation+1, b.ttl)})
	return m
}

func (b *RFC2136Traffic) prerequisites(state TrafficState) *dns.Msg {
	m := new(dns.Msg).SetUpdate(b.zone)
	m.Used([]dns.RR{b.markerRR(state.Generation, 0)})
	for _, host := range b.hosts {
		have4, have6 := false, false
		rrs := dnsAddressRRs(host, 0, state.Addresses)
		for _, rr := range rrs {
			if rr.Header().Rrtype == dns.TypeA {
				have4 = true
			} else {
				have6 = true
			}
		}
		m.Used(rrs)
		if !have4 {
			m.RRsetNotUsed([]dns.RR{dnsOwner(host, dns.TypeA)})
		}
		if !have6 {
			m.RRsetNotUsed([]dns.RR{dnsOwner(host, dns.TypeAAAA)})
		}
		m.RRsetNotUsed([]dns.RR{dnsOwner(host, dns.TypeCNAME), dnsOwner(host, dns.TypeHTTPS), dnsOwner(host, dns.TypeSVCB)})
	}
	m.RRsetNotUsed([]dns.RR{dnsOwner(b.marker, dns.TypeCNAME)})
	for _, name := range b.guardNames {
		m.RRsetNotUsed([]dns.RR{dnsOwner(name, dns.TypeDNAME)})
		if name != b.zone {
			m.RRsetNotUsed([]dns.RR{dnsOwner(name, dns.TypeNS)})
		}
	}
	return m
}

func dnsOwner(name string, typ uint16) *dns.ANY {
	return &dns.ANY{Hdr: dns.RR_Header{Name: name, Rrtype: typ}}
}

func dnsAddressRRs(host string, ttl uint32, addresses []netip.Addr) []dns.RR {
	rrs := make([]dns.RR, 0, len(addresses))
	for _, addr := range addresses {
		h := dns.RR_Header{Name: host, Class: dns.ClassINET, Ttl: ttl}
		if addr.Is4() {
			h.Rrtype = dns.TypeA
			rrs = append(rrs, &dns.A{Hdr: h, A: addr.AsSlice()})
		} else {
			h.Rrtype = dns.TypeAAAA
			rrs = append(rrs, &dns.AAAA{Hdr: h, AAAA: addr.AsSlice()})
		}
	}
	return rrs
}

func (b *RFC2136Traffic) markerRR(gen int64, ttl uint32) *dns.TXT {
	value := b.markerPrefix + strconv.FormatInt(gen, 10)
	return &dns.TXT{Hdr: dns.RR_Header{Name: b.marker, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: ttl}, Txt: []string{value}}
}

func (b *RFC2136Traffic) query(ctx context.Context, name string, typ uint16) ([]dns.RR, error) {
	m := new(dns.Msg).SetQuestion(name, typ)
	m.RecursionDesired = false
	r, err := b.exchange(ctx, m)
	if err != nil {
		return nil, err
	}
	if !r.Authoritative || r.Rcode != dns.RcodeSuccess || !dnsQuestionMatches(r.Question, m.Question) {
		return nil, fmt.Errorf("%w: invalid authoritative answer for %s %s", ErrSwitchConflict, name, dns.TypeToString[typ])
	}
	if len(r.Answer) > maxDNSAddresses {
		return nil, fmt.Errorf("%w: oversized DNS RRset for %s", ErrSwitchConflict, name)
	}
	for _, rr := range r.Answer {
		h := rr.Header()
		if !strings.EqualFold(h.Name, name) || h.Class != dns.ClassINET || h.Rrtype != typ {
			return nil, fmt.Errorf("%w: unexpected or aliased DNS answer for %s", ErrSwitchConflict, name)
		}
	}
	if typ == dns.TypeSOA && len(r.Answer) != 1 {
		return nil, fmt.Errorf("%w: no exact zone SOA", ErrSwitchConflict)
	}
	if len(r.Answer) == 0 {
		// Accept NODATA only with negative authority from this exact zone.
		var count int
		for _, rr := range r.Ns {
			if soa, ok := rr.(*dns.SOA); ok && soa.Hdr.Class == dns.ClassINET && strings.EqualFold(soa.Hdr.Name, b.zone) {
				count++
			}
		}
		if count != 1 {
			return nil, fmt.Errorf("%w: no zone SOA for negative answer at %s", ErrSwitchConflict, name)
		}
	}
	return r.Answer, nil
}

func (b *RFC2136Traffic) update(ctx context.Context, m *dns.Msg) error {
	r, err := b.exchange(ctx, m)
	if err != nil {
		if len(m.Ns) > 0 && !errors.Is(err, ErrInput) {
			return errors.Join(ErrSwitchUnsettled, err)
		}
		return err
	}
	// RFC 2136 §3.8 allows empty or echoed response sections.
	if (len(r.Question) != 0 && !dnsQuestionMatches(r.Question, m.Question)) ||
		!dnsEmptyOrEcho(r.Answer, m.Answer) || !dnsEmptyOrEcho(r.Ns, m.Ns) {
		return fmt.Errorf("%w: malformed DNS UPDATE response", ErrSwitchUnsettled)
	}
	// Only a well-formed, authenticated rejection of the mutating UPDATE
	// attests to non-application. Readback probes cannot provide that proof.
	rejected := func(err error) error {
		if len(m.Ns) != 0 {
			return errors.Join(ErrSwitchWriteRejected, err)
		}
		return err
	}
	switch r.Rcode {
	case dns.RcodeSuccess:
		return nil
	case dns.RcodeNameError, dns.RcodeYXDomain, dns.RcodeYXRrset, dns.RcodeNXRrset:
		return rejected(fmt.Errorf("%w: DNS prerequisite refused (%s)", ErrSwitchConflict, dns.RcodeToString[r.Rcode]))
	case dns.RcodeFormatError, dns.RcodeRefused, dns.RcodeNotImplemented, dns.RcodeNotAuth, dns.RcodeNotZone:
		return rejected(fmt.Errorf("DNS UPDATE rejected (%s)", dns.RcodeToString[r.Rcode]))
	default:
		return fmt.Errorf("%w: DNS UPDATE returned rcode %d", ErrSwitchUnsettled, r.Rcode)
	}
}

func dnsEmptyOrEcho(got, want []dns.RR) bool {
	return len(got) == 0 || slices.EqualFunc(got, want, func(a, b dns.RR) bool {
		// The library represents an empty typed RR differently when
		// constructed for UPDATE versus parsed back from the wire.
		if _, empty := b.(*dns.ANY); empty {
			ah, bh := a.Header(), b.Header()
			return ah.Rdlength == 0 && ah.Rrtype == bh.Rrtype && ah.Class == bh.Class &&
				ah.Ttl == bh.Ttl && strings.EqualFold(ah.Name, bh.Name)
		}
		var ab, bb [512]byte // bounded names and A/AAAA/marker RDATA
		an, ae := dns.PackRR(a, ab[:], 0, nil, false)
		bn, be := dns.PackRR(b, bb[:], 0, nil, false)
		return ae == nil && be == nil && bytes.Equal(ab[:an], bb[:bn])
	})
}

func dnsQuestionMatches(got, want []dns.Question) bool {
	return len(got) == 1 && len(want) == 1 && strings.EqualFold(got[0].Name, want[0].Name) && got[0].Qclass == want[0].Qclass && got[0].Qtype == want[0].Qtype
}

func (b *RFC2136Traffic) exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	m.SetTsig(b.key, dns.HmacSHA256, 60, time.Now().Unix())
	if m.Len() > maxDNSUpdateSize {
		return nil, fmt.Errorf("%w: RFC 2136 message too large", ErrInput)
	}
	conn, err := b.client.DialContext(ctx, b.primary)
	if err != nil {
		return nil, fmt.Errorf("dial DNS primary: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	r, _, err := b.client.ExchangeWithConnContext(ctx, m, conn)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("exchange with DNS primary: %w", err)
	}
	if !r.Response || r.Opcode != m.Opcode || r.Truncated {
		return nil, errors.New("invalid DNS response header")
	}
	// The library verifies a present TSIG (including the request MAC) but
	// does not require one. Require the expected identity, algorithm and MAC.
	ts := r.IsTsig()
	if ts == nil || ts.Hdr.Name != b.key || ts.Algorithm != dns.HmacSHA256 || ts.Error != 0 ||
		ts.OrigId != m.Id || ts.MACSize != 32 || ts.OtherLen != 0 || ts.Hdr.Class != dns.ClassANY || ts.Hdr.Ttl != 0 {
		return nil, errors.New("DNS reply lacks a valid HMAC-SHA256 TSIG")
	}
	for _, rr := range r.Extra[:len(r.Extra)-1] {
		if rr.Header().Rrtype == dns.TypeTSIG {
			return nil, errors.New("DNS reply contains multiple TSIG records")
		}
	}
	return r, nil
}
