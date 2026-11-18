package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/cutover"
	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

const dir = "/fixture/"
const operation = "isolated-two-node"
const baseGeneration int64 = 7

func trafficConfig(primary, secret string) cutover.RFC2136Config {
	return cutover.RFC2136Config{Primary: primary, Zone: zone, Hosts: hosts, Marker: "epoch." + zone, KeyName: "switch-key." + zone,
		Secret: strings.TrimSpace(secret), TTL: 1, Timeout: 4 * time.Second}
}

// Generate the exact marker using the production adapter, not a parallel hash implementation.
func seedDNS(path, sourceIP string) error {
	data, err := os.ReadFile(path + "/tsig")
	if err != nil {
		return err
	}
	cfg := trafficConfig("127.0.0.1:53", string(data))
	traffic, err := cutover.NewRFC2136Traffic(cfg)
	if err != nil {
		return err
	}
	marker, err := traffic.MarkerValue(baseGeneration)
	if err != nil {
		return err
	}
	conf := fmt.Sprintf(`options { directory "/fixture"; listen-on port 53 { any; }; listen-on-v6 { none; }; recursion no; dnssec-validation no; notify no; };
controls { };
key "%s." { algorithm hmac-sha256; secret %q; };
zone "%s." { type primary; file "zone.db"; update-policy {
 grant %s. name %s. A AAAA;
 grant %s. name %s. A AAAA;
 grant %s. name %s. TXT;
}; };`, cfg.KeyName, cfg.Secret, zone, cfg.KeyName, hosts[0], cfg.KeyName, hosts[1], cfg.KeyName, cfg.Marker)
	if err := os.WriteFile(path+"/named.conf", []byte(conf), 0600); err != nil {
		return err
	}
	f, err := os.OpenFile(path+"/zone.db", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, `$ORIGIN %s.
$TTL 1
@ IN SOA ns.%s. hostmaster.%s. 1 60 60 600 1
@ IN NS ns.%s.
ns IN A 192.0.2.53
%s. IN A %s
%s. IN A %s
%s. IN TXT %q
`, zone, zone, zone, zone, hosts[0], sourceIP, hosts[1], sourceIP, cfg.Marker, marker)
	return err
}

type fixture struct {
	*cutover.GateNodes
	traffic *cutover.RFC2136Traffic
	roots   *x509.CertPool
	http    *http.Client
	spec    cutover.SwitchSpec
}

func drive(stage string) error {
	roots := x509.NewCertPool()
	ca, err := os.ReadFile(dir + "ca.pem")
	if err != nil {
		return err
	}
	if !roots.AppendCertsFromPEM(ca) {
		return fmt.Errorf("invalid fixture CA")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	gateNodes, err := cutover.OpenGateNodes(dir+"profiles.json", nodes...)
	if err != nil {
		return err
	}
	defer gateNodes.CloseIdleConnections()
	tsig, err := os.ReadFile(dir + "tsig")
	if err != nil {
		return err
	}
	traffic, err := cutover.NewRFC2136Traffic(trafficConfig(os.Getenv("FIXTURE_DNS"), string(tsig)))
	if err != nil {
		return err
	}
	f := &fixture{GateNodes: gateNodes, traffic: traffic, roots: roots, http: tlsClient(roots, netip.Addr{})}
	defer f.http.CloseIdleConnections()
	if stage == "enroll" {
		return f.enroll(ctx)
	}
	if stage == "prepare" {
		return f.prepare(ctx)
	}
	raw, err := os.ReadFile(dir + "spec.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &f.spec); err != nil {
		return err
	}
	probe, rows, err := f.probe(ctx)
	if err != nil {
		return err
	}
	journal, err := cutover.OpenFileJournal(dir + "journal")
	if err != nil {
		return err
	}
	observed := &checkedJournal{SwitchJournal: journal, f: f}
	coordinator := cutover.NewCoordinator(gateNodes, traffic, probe, observed)
	var progress cutover.SwitchProgress
	var active, inactive cutover.SwitchNode
	var generation int64
	var status cutover.SwitchStatus
	if stage == "forward" {
		progress, err = coordinator.Start(ctx, f.spec)
		active, inactive, generation, status = f.spec.Target, f.spec.Source, baseGeneration+1, cutover.SwitchComplete
	} else {
		progress, err = coordinator.Rollback(ctx, operation)
		active, inactive, generation, status = f.spec.Source, f.spec.Target, baseGeneration+2, cutover.SwitchRolledBack
	}
	if err != nil {
		return fmt.Errorf("%s (%s, pending %s): %w", stage, progress.Status, progress.Pending, err)
	}
	if progress.Status != status || progress.Pending != "" || observed.barriers != 1 {
		return fmt.Errorf("%s: expected final status %s, no pending action and one both-blocked barrier; got %s, %q and %d", stage, status, progress.Status, progress.Pending, observed.barriers)
	}
	// Both gates advance once during each direction.
	gateGeneration := generation - baseGeneration + 1
	if err := f.verify(ctx, probe, rows, active, inactive, generation, gateGeneration, gateGeneration); err != nil {
		return err
	}
	fmt.Printf("%s: %s serves all %d canaries over trusted TLS and ordinary DNS; %s refuses all; DNS generation %d\n", stage, active.ID, len(mappings), inactive.ID, generation)
	return nil
}

// Check the actual data plane at the durable barrier where both sides are
// supposed to be blocked. Fail the operation before it can make either side
// public if either node served any of the fresh requests.
type checkedJournal struct {
	cutover.SwitchJournal
	f        *fixture
	barriers int
}

func (j *checkedJournal) WithOperation(ctx context.Context, id string, run func(*cutover.SwitchProgress, func(cutover.SwitchProgress) error) error) error {
	return j.SwitchJournal.WithOperation(ctx, id, func(current *cutover.SwitchProgress, save func(cutover.SwitchProgress) error) error {
		return run(current, func(p cutover.SwitchProgress) error {
			if p.Pending == "" && (p.Status == cutover.SwitchSourceBlocked || p.Status == cutover.SwitchRestoring) &&
				p.SourceGates[hosts[0]].Phase == store.PublicationBlocked && p.TargetGates[hosts[0]].Phase == store.PublicationBlocked {
				for _, n := range []cutover.SwitchNode{p.Spec.Source, p.Spec.Target} {
					if err := j.f.refusal(ctx, n); err != nil {
						return err
					}
				}
				j.barriers++
				fmt.Println("barrier: both nodes returned 404 for every business canary")
			}
			return save(p)
		})
	})
}

func tlsClient(roots *x509.CertPool, forced netip.Addr) *http.Client {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DisableKeepAlives: true}
	if forced.IsValid() {
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(forced.String(), "443"))
		}
	}
	return &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func request(ctx context.Context, client *http.Client, method, endpoint, token string, data any, status int, result any) error {
	var payload []byte
	var err error
	if data != nil {
		payload, err = json.Marshal(data)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		return fmt.Errorf("%s %s: got HTTP %d, want %d", method, endpoint, resp.StatusCode, status)
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result)
	}
	return nil
}

func retry(ctx context.Context, label string, fn func() error) error {
	for {
		err := fn()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %v (%w)", label, err, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func adminToken(id string) (string, error) {
	raw, err := os.ReadFile(dir + id + ".token")
	return strings.TrimSpace(string(raw)), err
}

func (f *fixture) enroll(ctx context.Context) error {
	for _, id := range nodes {
		token, err := adminToken(id)
		if err != nil {
			return err
		}
		if err := retry(ctx, id+" health", func() error {
			return request(ctx, f.http, http.MethodGet, management(id)+"/healthz", "", nil, 200, nil)
		}); err != nil {
			return err
		}
		var issued struct {
			Token string `json:"token"`
		}
		if err := request(ctx, f.http, http.MethodPost, management(id)+"/api/v1/enrollment-tokens", token, map[string]string{"expires_in": "5m"}, 201, &issued); err != nil {
			return err
		}
		if issued.Token == "" {
			return fmt.Errorf("%s enrollment response was empty", id)
		}
		if err := os.WriteFile(dir+id+".enrollment", []byte(issued.Token), 0600); err != nil {
			return err
		}
	}
	return nil
}

func (f *fixture) prepare(ctx context.Context) error {
	f.spec = cutover.SwitchSpec{OperationID: operation, Hosts: hosts}
	for _, id := range nodes {
		token, err := adminToken(id)
		if err != nil {
			return err
		}
		base := management(id) + "/api/v1/"
		var agents []domain.Agent
		if err := retry(ctx, id+" agent enrollment", func() error {
			if err := request(ctx, f.http, http.MethodGet, base+"clients", token, nil, 200, &agents); err != nil {
				return err
			}
			if len(agents) != 1 || agents[0].ID == "" {
				return fmt.Errorf("expected one enrolled agent, got %d", len(agents))
			}
			return nil
		}); err != nil {
			return err
		}
		ip, err := netip.ParseAddr(os.Getenv("FIXTURE_" + strings.ToUpper(id)))
		if err != nil {
			return err
		}
		n := cutover.SwitchNode{ID: id, ClientID: agents[0].ID, ManagementHost: management(id), Addresses: []netip.Addr{ip}, Generations: map[string]int64{}}
		for _, m := range mappings {
			for _, host := range []string{m.host, preview(id, m.host)} {
				route := domain.Route{ClientID: n.ClientID, Name: host + m.prefix, Protocol: domain.ProtocolHTTPS, Domain: host, PathPrefix: m.prefix,
					StripPath: m.prefix != "", LocalHost: "127.0.0.1", LocalPort: m.port, TunnelGroup: "web", Enabled: true}
				if err := request(ctx, f.http, http.MethodPost, base+"routes", token, route, 201, nil); err != nil {
					return err
				}
			}
		}
		snapshot, err := f.InspectNode(ctx, id, hosts)
		if err != nil {
			return err
		}
		var group []store.PublicationGateExpectation
		for _, host := range hosts {
			group = append(group, store.PublicationGateExpectation{Host: host, Expected: snapshot.Gates[host]})
			n.Generations[host] = 1
		}
		phase := store.PublicationFrozen
		if id == "target" {
			phase = store.PublicationBlocked
		}
		if err := f.CompareNode(ctx, id, group, store.PublicationPolicy{Phase: phase, OperationID: operation, ClientID: n.ClientID}); err != nil {
			return err
		}
		if id == "source" {
			f.spec.Source = n
		} else {
			f.spec.Target = n
		}
	}
	probe, rows, err := f.probe(ctx)
	if err != nil {
		return err
	}
	if err := retry(ctx, "isolated target through preview routes and real SSH", func() error {
		return probe.VerifyHTTPS(ctx, f.spec.Target, hosts, rows["target"], cutover.ProbeIsolated)
	}); err != nil {
		return err
	}
	if err := retry(ctx, "source and target baseline", func() error {
		return f.verify(ctx, probe, rows, f.spec.Source, f.spec.Target, baseGeneration, 1, 1)
	}); err != nil {
		return err
	}
	fmt.Printf("baseline: source serves %d canaries; target refuses business hosts but serves matching protected preview canaries; DNS generation %d\n", len(mappings), baseGeneration)
	raw, err := json.Marshal(f.spec)
	if err != nil {
		return err
	}
	return os.WriteFile(dir+"spec.json", raw, 0600)
}

func (f *fixture) probe(ctx context.Context) (*cutover.HTTPSProbe, map[string][]cutover.MappingIdentity, error) {
	var jobs []cutover.HTTPSRouteProbe
	rows := make(map[string][]cutover.MappingIdentity)
	for _, n := range []cutover.SwitchNode{f.spec.Source, f.spec.Target} {
		snapshot, err := f.InspectNode(ctx, n.ID, hosts)
		if err != nil {
			return nil, nil, err
		}
		previewHosts := make([]string, 0, len(hosts))
		for _, host := range hosts {
			previewHosts = append(previewHosts, preview(n.ID, host))
		}
		previews, err := f.InspectNode(ctx, n.ID, previewHosts)
		if err != nil {
			return nil, nil, err
		}
		previewIDs := make(map[mapping]string)
		for _, r := range previews.Routes {
			for _, m := range mappings {
				if r.Domain != preview(n.ID, m.host) || r.PathPrefix != m.prefix {
					continue
				}
				if previewIDs[m] != "" || !r.Enabled || r.LocalPort != m.port || r.StripPath != (m.prefix != "") {
					return nil, nil, fmt.Errorf("%s: wrong preview route for %s%s", n.ID, m.host, m.prefix)
				}
				previewIDs[m] = r.ID
			}
		}
		if len(previewIDs) != len(mappings) || len(previews.Routes) != len(mappings) {
			return nil, nil, fmt.Errorf("%s: incomplete preview route membership", n.ID)
		}
		matched := make(map[mapping]bool)
		for _, r := range snapshot.Routes {
			for _, m := range mappings {
				if r.Domain != m.host || r.PathPrefix != m.prefix {
					continue
				}
				if matched[m] || !r.Enabled || r.LocalPort != m.port || r.StripPath != (m.prefix != "") {
					return nil, nil, fmt.Errorf("%s: wrong route for %s%s", n.ID, m.host, m.prefix)
				}
				matched[m] = true
				row := cutover.MappingIdentity{ID: r.ID, Host: r.Domain, PathPrefix: r.PathPrefix, LocalHost: r.LocalHost, TunnelGroup: r.TunnelGroup,
					LocalPort: r.LocalPort, RemotePort: r.RemotePort, DesiredRevision: r.DesiredRevision, Enabled: r.Enabled, StripPath: r.StripPath}
				rows[n.ID] = append(rows[n.ID], row)
				want := cutover.HTTPSResponseExpectation{Status: 200, SHA256: sha256.Sum256([]byte(body(n.ID, m.originPath)))}
				jobs = append(jobs, cutover.HTTPSRouteProbe{NodeID: n.ID, Route: row, Path: m.probe, PreviewHost: preview(n.ID, m.host),
					PreviewRouteID: previewIDs[m], PreviewAddress: netip.AddrPortFrom(n.Addresses[0], 443), PublicResponse: want, PreviewResponse: want})
			}
		}
		if len(matched) != len(mappings) || len(snapshot.Routes) != len(mappings) {
			return nil, nil, fmt.Errorf("%s: incomplete route membership", n.ID)
		}
	}
	probe, err := cutover.NewHTTPSProbe(cutover.HTTPSProbeConfig{Routes: jobs, PublicRoots: f.roots, PreviewRoots: f.roots, Timeout: 5 * time.Second})
	return probe, rows, err
}

func (f *fixture) refusal(ctx context.Context, n cutover.SwitchNode) error {
	client := tlsClient(f.roots, n.Addresses[0])
	defer client.CloseIdleConnections()
	for _, m := range mappings {
		endpoint := "https://" + m.host + m.probe
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("inactive %s on %s: %w", n.ID, endpoint, err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != 404 || string(raw) != "404 page not found\n" ||
			len(resp.Header.Values(cutover.HTTPSProbeNodeHeader)) != 0 || len(resp.Header.Values(cutover.HTTPSProbeRouteHeader)) != 0 {
			return fmt.Errorf("inactive %s on %s: expected an unmarked gateway 404 (got HTTP %d)", n.ID, endpoint, resp.StatusCode)
		}
	}
	return nil
}

func (f *fixture) verify(ctx context.Context, probe *cutover.HTTPSProbe, rows map[string][]cutover.MappingIdentity, active, inactive cutover.SwitchNode, generation, activeGate, inactiveGate int64) error {
	state, err := f.traffic.InspectTraffic(ctx, hosts)
	if err != nil {
		return err
	}
	if state.Generation != generation || !slices.Equal(state.Addresses, active.Addresses) {
		return fmt.Errorf("unexpected BIND state: %+v", state)
	}
	for _, check := range []struct {
		n          cutover.SwitchNode
		phase      store.PublicationPhase
		generation int64
	}{
		{active, store.PublicationFrozen, activeGate}, {inactive, store.PublicationBlocked, inactiveGate},
	} {
		s, err := f.InspectNode(ctx, check.n.ID, hosts)
		if err != nil {
			return err
		}
		for _, host := range hosts {
			want := store.PublicationGate{PublicationPolicy: store.PublicationPolicy{Phase: check.phase, OperationID: operation, ClientID: check.n.ClientID}, Generation: check.generation}
			if s.Gates[host] != want {
				return fmt.Errorf("%s gate %s: got %+v, want %+v", check.n.ID, host, s.Gates[host], want)
			}
		}
	}
	if err := probe.VerifyHTTPS(ctx, active, hosts, rows[active.ID], cutover.ProbePublic); err != nil {
		return err
	}
	return f.refusal(ctx, inactive)
}
