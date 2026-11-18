// Package cutover provides PortLoom inventory preflight and coordinated cutover.
package cutover

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lkhmm520/portloom/internal/domain"
)

type Request struct {
	Source, Target             string
	SourceClient, TargetClient string
	Hosts                      []string
	Group                      string
}

type Report struct {
	Status       string         `json:"status"`
	Source       string         `json:"source"`
	Target       string         `json:"target"`
	SourceClient string         `json:"source_client"`
	TargetClient string         `json:"target_client"`
	Group        string         `json:"group,omitempty"`
	Hosts        []string       `json:"hosts"`
	Routes       []RoutePreview `json:"routes"`
	Blockers     []Finding      `json:"blockers"`
	Checks       Checks         `json:"checks"`
	Execute      string         `json:"execute"`
}

type Checks struct {
	Inventory  string `json:"inventory"`
	SSH        string `json:"ssh"`
	ServiceTLS string `json:"service_tls"`
	Public     string `json:"public"`
	DNS        string `json:"dns"`
}

type RoutePreview struct {
	ID          string          `json:"id"`
	Client      string          `json:"client_id"`
	Host        string          `json:"host"`
	PathPrefix  string          `json:"path_prefix"`
	StripPath   bool            `json:"strip_path"`
	Protocol    domain.Protocol `json:"protocol"`
	PublicPort  int             `json:"public_port"`
	Enabled     bool            `json:"enabled"`
	LocalTarget string          `json:"local_target"`
	Reasons     []string        `json:"reasons"`
}

type Finding struct {
	Code     string `json:"code"`
	Host     string `json:"host,omitempty"`
	RouteID  string `json:"route_id,omitempty"`
	ClientID string `json:"client_id,omitempty"`
}

// Plan reads only the configured source and target's admin inventories. A clean
// preview is not an approval: the reads are not a transaction and do not prove
// native listener configuration, Agent trust, or data-plane reachability.
func Plan(ctx context.Context, configPath string, req Request) (Report, error) {
	if !domain.ValidAgentName(req.Source) || !domain.ValidAgentName(req.Target) || req.Source == req.Target ||
		strings.TrimSpace(req.SourceClient) == "" || strings.TrimSpace(req.TargetClient) == "" {
		return Report{}, fmt.Errorf("%w: select distinct profiles and two client IDs", ErrInput)
	}
	if (req.Group == "") == (len(req.Hosts) == 0) {
		return Report{}, fmt.Errorf("%w: select either one --group or one or more --host flags", ErrInput)
	}
	if req.Group != "" && !domain.ValidAgentName(req.Group) {
		return Report{}, fmt.Errorf("%w: invalid group name", ErrInput)
	}
	config, err := loadConfig(configPath)
	if err != nil {
		return Report{}, err
	}
	var hosts []string
	if req.Group != "" {
		group, ok := config.Groups[req.Group]
		if !ok {
			return Report{}, fmt.Errorf("%w: selected group does not exist", ErrInput)
		}
		hosts = group.Hosts // loadConfig has already validated and normalized every group.
	} else {
		hosts, err = normalizeHosts(req.Hosts)
		if err != nil {
			return Report{}, fmt.Errorf("--host: %w", err)
		}
	}
	sourceProfile, hasSource := config.Profiles[req.Source]
	targetProfile, hasTarget := config.Profiles[req.Target]
	if !hasSource || !hasTarget {
		return Report{}, fmt.Errorf("%w: selected profile does not exist", ErrInput)
	}
	source, err := newInventoryClient(sourceProfile, filepath.Dir(configPath))
	if err != nil {
		return Report{}, fmt.Errorf("source: %w", err)
	}
	defer source.http.CloseIdleConnections()
	target, err := newInventoryClient(targetProfile, filepath.Dir(configPath))
	if err != nil {
		return Report{}, fmt.Errorf("target: %w", err)
	}
	defer target.http.CloseIdleConnections()
	if originKey(source) == originKey(target) {
		return Report{}, fmt.Errorf("%w: source and target must use distinct API origins", ErrInput)
	}
	old, err := source.read(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("source inventory: %w", err)
	}
	next, err := target.read(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("target inventory: %w", err)
	}
	return compare(req, hosts, old, next, source.managementHost, target.managementHost,
		domain.NormalizeHost(source.endpoint.Hostname()), domain.NormalizeHost(target.endpoint.Hostname())), nil
}

func originKey(c *inventoryClient) string {
	port := c.endpoint.Port()
	if port == "" {
		port = "443"
	}
	if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}
	host := c.endpoint.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	return net.JoinHostPort(domain.NormalizeHost(host), port)
}

func compare(req Request, hosts []string, old, next inventory, managementHosts ...string) Report {
	report := Report{
		Status: "preview", Source: req.Source, Target: req.Target, SourceClient: req.SourceClient, TargetClient: req.TargetClient, Group: req.Group,
		Hosts: hosts, Routes: []RoutePreview{}, Blockers: []Finding{}, Execute: "unavailable",
		Checks: Checks{Inventory: "performed", SSH: "not_performed", ServiceTLS: "not_performed", Public: "not_performed", DNS: "not_performed"},
	}
	block := func(code, host, routeID, clientID string) {
		report.Blockers = append(report.Blockers, Finding{Code: code, Host: host, RouteID: routeID, ClientID: clientID})
	}
	hasClient := func(clients []domain.Agent, id string) bool {
		for _, client := range clients {
			if client.ID == id {
				return true
			}
		}
		return false
	}
	if !hasClient(old.clients, req.SourceClient) {
		block("source_client_not_found", "", "", req.SourceClient)
	}
	if !hasClient(next.clients, req.TargetClient) {
		block("target_client_not_found", "", "", req.TargetClient)
	}
	selected := make(map[string]bool)
	reserved := make(map[string]bool)
	targetConflicts := make(map[string]bool)
	sourceHosts := make(map[string]bool)
	for _, host := range managementHosts {
		reserved[host] = true
	}
	for _, host := range hosts {
		selected[host] = true
		if reserved[host] {
			block("management_host", host, "", "")
		}
	}
	for _, r := range next.routes {
		host := domain.NormalizeHost(r.Domain)
		if r.Protocol.IsWeb() && selected[host] {
			block("target_host_owned", host, r.ID, r.ClientID)
			targetConflicts[host] = true
		}
	}
	for _, r := range old.routes {
		host := domain.NormalizeHost(r.Domain)
		if !selected[host] {
			continue
		}
		if r.ClientID == req.SourceClient {
			sourceHosts[host] = true
		}
		row := RoutePreview{ID: r.ID, Client: r.ClientID, Host: host, PathPrefix: r.PathPrefix, StripPath: r.StripPath,
			Protocol: r.Protocol, PublicPort: r.EffectivePublicPort(), Enabled: *r.Enabled, Reasons: []string{}}
		addReason := func(code string) {
			row.Reasons = append(row.Reasons, code)
			block(code, host, r.ID, r.ClientID)
		}
		if r.ClientID != req.SourceClient {
			addReason("other_source_owner")
		}
		if r.Protocol != domain.ProtocolHTTPS || r.EffectivePublicPort() != 443 {
			addReason("unsupported_public_endpoint")
		}
		if !domain.ValidHost(r.LocalHost) || r.LocalPort < 1 || r.LocalPort > 65535 {
			addReason("invalid_local_target")
		} else {
			row.LocalTarget = net.JoinHostPort(r.LocalHost, strconv.Itoa(r.LocalPort))
		}
		if reserved[host] {
			row.Reasons = append(row.Reasons, "management_host")
		}
		if targetConflicts[host] {
			row.Reasons = append(row.Reasons, "target_host_owned")
		}
		if len(row.Reasons) == 0 {
			row.Reasons = append(row.Reasons, "source_inventory_match")
		}
		report.Routes = append(report.Routes, row)
	}
	for _, host := range hosts {
		if !sourceHosts[host] {
			block("source_host_not_found_for_client", host, "", req.SourceClient)
		}
	}
	sort.Slice(report.Routes, func(i, j int) bool {
		a, b := report.Routes[i], report.Routes[j]
		return strings.Compare(a.Host, b.Host) < 0 || a.Host == b.Host && (a.PathPrefix < b.PathPrefix || a.PathPrefix == b.PathPrefix && a.ID < b.ID)
	})
	sort.Slice(report.Blockers, func(i, j int) bool {
		a, b := report.Blockers[i], report.Blockers[j]
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.RouteID != b.RouteID {
			return a.RouteID < b.RouteID
		}
		return a.ClientID < b.ClientID
	})
	if len(report.Blockers) > 0 {
		report.Status = "blocked"
	}
	return report
}
