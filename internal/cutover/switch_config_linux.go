package cutover

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"

	"strings"
	"time"
	"unicode/utf8"

	"github.com/lkhmm520/portloom/internal/domain"
	"golang.org/x/sys/unix"
)

// One private, operator-owned config per exact preprovisioned operation.
type switchConfig struct {
	Enabled     bool              `json:"enabled"`
	OperationID string            `json:"operation_id"`
	JournalDir  string            `json:"journal_dir"`
	Hosts       []string          `json:"hosts"`
	Source      switchNodeConfig  `json:"source"`
	Target      switchNodeConfig  `json:"target"`
	DNS         switchDNSConfig   `json:"dns"`
	HTTPS       switchHTTPSConfig `json:"https"`
}

type switchNodeConfig struct {
	ID          string           `json:"id"`
	Profile     profile          `json:"profile"`
	ClientID    string           `json:"client_id"`
	Generations map[string]int64 `json:"generations"`
	Addresses   []netip.Addr     `json:"addresses"`
}

type switchDNSConfig struct {
	Primary    string `json:"primary"`
	Zone       string `json:"zone"`
	Marker     string `json:"marker"`
	KeyName    string `json:"key_name"`
	SecretFile string `json:"secret_file"`
	TTL        uint32 `json:"ttl"`
}

type switchHTTPSConfig struct {
	PublicCAFile  string              `json:"public_ca_file"`
	PreviewCAFile string              `json:"preview_ca_file"`
	Routes        []switchProbeConfig `json:"routes"`
}

type switchProbeConfig struct {
	NodeID         string               `json:"node_id"`
	Route          MappingIdentity      `json:"route"`
	Path           string               `json:"path"`
	PreviewHost    string               `json:"preview_host"`
	PreviewRouteID string               `json:"preview_route_id"`
	PreviewAddress netip.AddrPort       `json:"preview_address"`
	Public         switchResponseConfig `json:"public"`
	Preview        switchResponseConfig `json:"preview"`
}

type switchResponseConfig struct {
	Status int    `json:"status"`
	SHA256 string `json:"sha256"`
}

// SwitchSetup is a validated local construction; opening it never contacts a
// node or the DNS primary. The digest excludes rotatable credentials but binds
// the exact config bytes, resolved config path and explicitly supplied CA bytes.
type SwitchSetup struct {
	Spec         SwitchSpec
	Confirmation string
	coordinator  *Coordinator
	nodes        *GateNodes
}

func OpenSwitchSetup(path, id string) (*SwitchSetup, error) {
	if !validCLIOperation(id) {
		return nil, fmt.Errorf("%w: invalid operation ID", ErrInput)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid switch config path", ErrInput)
	}
	data, err := readPrivateSwitchFile(path, configLimit)
	if err != nil {
		return nil, fmt.Errorf("%w: switch config must be an owned mode-0600 regular file", ErrInput)
	}
	var cfg switchConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if !utf8.Valid(data) || dec.Decode(&cfg) != nil || dec.Decode(new(any)) != io.EOF || !uniqueConfigKeys(json.NewDecoder(bytes.NewReader(data))) {
		return nil, fmt.Errorf("%w: invalid switch JSON (unknown or duplicate field, malformed value or trailing data)", ErrInput)
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("%w: switch config requires enabled: true", ErrInput)
	}
	if cfg.OperationID != id {
		return nil, fmt.Errorf("%w: requested operation must match switch config operation_id", ErrInput)
	}
	spec, err := normalizeSwitchSpec(SwitchSpec{OperationID: id, Hosts: cfg.Hosts, Source: cfg.Source.node(), Target: cfg.Target.node()})
	if err != nil {
		return nil, err
	}
	if cfg.JournalDir == "" {
		return nil, fmt.Errorf("%w: journal_dir is required", ErrInput)
	}
	resolve := func(name string) string {
		if filepath.IsAbs(name) {
			return filepath.Clean(name)
		}
		return filepath.Join(filepath.Dir(path), name)
	}
	journal, err := OpenFileJournal(resolve(cfg.JournalDir))
	if err != nil {
		return nil, fmt.Errorf("%w: journal_dir must exist, be owned by the effective UID and have mode 0700", ErrInput)
	}
	// Fixed-order, fixed-length hashes with role separation, so different CA
	// concatenations or an absent CA cannot produce the same serialized binding.
	hashes := [][sha256.Size]byte{sha256.Sum256([]byte(path)), sha256.Sum256(data)}
	loadRoots := func(name string, system bool) (*x509.CertPool, error) {
		if name == "" {
			hashes = append(hashes, [sha256.Size]byte{})
			return nil, nil
		}
		pem, err := readLocal(resolve(name), caLimit, false)
		if err != nil {
			return nil, fmt.Errorf("%w: unreadable CA file", ErrInput)
		}
		hashes = append(hashes, sha256.Sum256(pem))
		roots := x509.NewCertPool()
		if system {
			roots, err = x509.SystemCertPool()
			if err != nil {
				return nil, fmt.Errorf("%w: could not load system CA roots", ErrInput)
			}
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%w: CA file contains no PEM certificate", ErrInput)
		}
		return roots, nil
	}
	nodes := &GateNodes{clients: make(map[string]*inventoryClient, 2)}
	opened := false
	defer func() {
		if !opened {
			nodes.CloseIdleConnections()
		}
	}()
	for _, n := range []switchNodeConfig{cfg.Source, cfg.Target} {
		if !domain.ValidAgentName(n.ID) || strings.Contains(n.ID, ",") {
			return nil, fmt.Errorf("%w: invalid node ID", ErrInput)
		}
		if n.Profile.TokenFile == "" {
			return nil, fmt.Errorf("%w: token_file is required", ErrInput)
		}
		if _, err := readPrivateSwitchFile(resolve(n.Profile.TokenFile), tokenLimit); err != nil {
			return nil, fmt.Errorf("%w: token_file must be an owned mode-0600 regular file", ErrInput)
		}
		roots, err := loadRoots(n.Profile.CAFile, true)
		if err != nil {
			return nil, err
		}
		n.Profile.CAFile = "" // Use precisely the CA bytes included in the confirmation.
		c, err := newInventoryClient(n.Profile, filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		c.http.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
		nodes.clients[n.ID] = c
	}
	from, to := nodes.clients[spec.Source.ID], nodes.clients[spec.Target.ID]
	if from.managementHost == to.managementHost || originKey(from) == originKey(to) {
		return nil, fmt.Errorf("%w: source and target require distinct API origins and management hosts", ErrInput)
	}
	if cfg.DNS.SecretFile == "" {
		return nil, fmt.Errorf("%w: DNS secret_file is required", ErrInput)
	}
	secret, err := readPrivateSwitchFile(resolve(cfg.DNS.SecretFile), 2048)
	if err != nil {
		return nil, fmt.Errorf("%w: DNS secret_file must be an owned mode-0600 regular file", ErrInput)
	}
	traffic, err := NewRFC2136Traffic(RFC2136Config{Primary: cfg.DNS.Primary, Zone: cfg.DNS.Zone, Hosts: spec.Hosts, Marker: cfg.DNS.Marker, KeyName: cfg.DNS.KeyName, Secret: strings.TrimSpace(string(secret)), TTL: cfg.DNS.TTL})
	if err != nil {
		return nil, err
	}

	public, err := loadRoots(cfg.HTTPS.PublicCAFile, false)
	if err != nil {
		return nil, err
	}
	preview, err := loadRoots(cfg.HTTPS.PreviewCAFile, false)
	if err != nil {
		return nil, err
	}
	probeConfig := HTTPSProbeConfig{PublicRoots: public, PreviewRoots: preview, Timeout: 30 * time.Second}
	routes := map[string][]MappingIdentity{}
	for _, r := range cfg.HTTPS.Routes {
		if r.NodeID != spec.Source.ID && r.NodeID != spec.Target.ID || r.PreviewHost == spec.Source.ManagementHost || r.PreviewHost == spec.Target.ManagementHost {
			return nil, fmt.Errorf("%w: HTTPS canary node or preview host outside allowed scope", ErrInput)
		}
		var public, preview HTTPSResponseExpectation
		if !decodeSwitchResponse(r.Public, &public) || !decodeSwitchResponse(r.Preview, &preview) {
			return nil, fmt.Errorf("%w: each canary requires public and preview status and a 64-digit SHA-256", ErrInput)
		}
		routes[r.NodeID] = append(routes[r.NodeID], r.Route)
		probeConfig.Routes = append(probeConfig.Routes, HTTPSRouteProbe{NodeID: r.NodeID, Route: r.Route, Path: r.Path, PreviewHost: r.PreviewHost, PreviewRouteID: r.PreviewRouteID, PreviewAddress: r.PreviewAddress, PublicResponse: public, PreviewResponse: preview})
	}
	probe, err := NewHTTPSProbe(probeConfig)
	if err != nil {
		return nil, err
	}
	for _, n := range []SwitchNode{spec.Source, spec.Target} {
		if len(n.Addresses) > maxDNSAddresses {
			return nil, fmt.Errorf("%w: too many node addresses", ErrInput)
		}
		for _, ip := range n.Addresses {
			if !validProbeIP(ip) {
				return nil, fmt.Errorf("%w: invalid HTTPS peer address", ErrInput)
			}
		}
		if _, err := probe.plan(n, spec.Hosts, routes[n.ID]); err != nil {
			return nil, err
		}
	}
	hash := sha256.New()
	hash.Write([]byte("portloomctl-switch-v1\x00"))
	for _, value := range hashes {
		hash.Write(value[:])
	}
	confirmation := hex.EncodeToString(hash.Sum(nil))
	bound := &switchBoundJournal{FileJournal: journal, confirmation: confirmation}
	checkedNodes := switchProbeNodes{GateNodes: nodes, probe: probe, spec: spec}
	opened = true
	return &SwitchSetup{Spec: spec, Confirmation: confirmation, coordinator: NewCoordinator(checkedNodes, traffic, probe, bound), nodes: nodes}, nil
}

func (n switchNodeConfig) node() SwitchNode {
	return SwitchNode{ID: n.ID, ClientID: n.ClientID, ManagementHost: n.Profile.ManagementHost, Generations: n.Generations, Addresses: n.Addresses}
}

func validCLIOperation(id string) bool {
	return len(id) <= 120 && domain.ValidAgentName(id) && !strings.Contains(id, ",")
}

func decodeSwitchResponse(cfg switchResponseConfig, into *HTTPSResponseExpectation) bool {
	if len(cfg.SHA256) != hex.EncodedLen(sha256.Size) {
		return false
	}
	n, err := hex.Decode(into.SHA256[:], []byte(cfg.SHA256))
	into.Status = cfg.Status
	return err == nil && n == sha256.Size
}

func readPrivateSwitchFile(path string, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "private switch file")
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return nil, errors.New("unsafe private switch file")
	}
	return readLimited(f, limit)
}

type switchProbeNodes struct {
	*GateNodes
	probe *HTTPSProbe
	spec  SwitchSpec
}

// Ensure rollback and forward both have exact canary coverage before permitting
// any write, using the same authenticated inventory snapshot as the state machine.
func (n switchProbeNodes) InspectNode(ctx context.Context, id string, hosts []string) (NodeSnapshot, error) {
	snap, err := n.GateNodes.InspectNode(ctx, id, hosts)
	if err != nil {
		return snap, err
	}
	node := n.spec.Source
	if id == n.spec.Target.ID {
		node = n.spec.Target
	}
	routes, err := projectRoutes(hosts, node, snap.Routes)
	if err != nil {
		return NodeSnapshot{}, err
	}
	if _, err := n.probe.plan(node, hosts, routes); err != nil {
		return NodeSnapshot{}, fmt.Errorf("%w: %v", ErrSwitchConflict, err)
	}
	return snap, nil
}

func (s *SwitchSetup) Close() { s.nodes.CloseIdleConnections() }

func (s *SwitchSetup) Execute(ctx context.Context, verb, confirmation string) (SwitchProgress, error) {
	if confirmation != s.Confirmation {
		return SwitchProgress{}, fmt.Errorf("%w: confirmation does not match the checked configuration", ErrInput)
	}
	switch verb {
	case "switch":
		return s.coordinator.Start(ctx, s.Spec)
	case "settle":
		return s.coordinator.SettlePending(ctx, s.Spec.OperationID)
	case "rollback":
		return s.coordinator.Rollback(ctx, s.Spec.OperationID)
	default:
		return SwitchProgress{}, fmt.Errorf("%w: unknown switch action", ErrInput)
	}
}

// ReadSwitchStatus reads the local journal only. It never opens configuration,
// credentials or network connections and remains usable after config drift.
func ReadSwitchStatus(ctx context.Context, dir, id string) (SwitchProgress, error) {
	if !validCLIOperation(id) {
		return SwitchProgress{}, fmt.Errorf("%w: invalid operation ID", ErrInput)
	}
	journal, err := OpenFileJournal(dir)
	if err != nil {
		return SwitchProgress{}, fmt.Errorf("%w: invalid journal directory", ErrInput)
	}
	var status SwitchProgress
	err = journal.WithOperation(ctx, id, func(p *SwitchProgress, _ func(SwitchProgress) error) error {
		if p == nil {
			return fmt.Errorf("%w: operation not found", ErrSwitchConflict)
		}
		status = *p
		return nil
	})
	return status, err
}

// Binding is persisted before the first journal record under the same operation
// lock. A record without a binding cannot be adopted. A crash between writes can
// leave only the binding; only the original configuration may retry in that case.
type switchBoundJournal struct {
	*FileJournal
	confirmation string
}

func (j *switchBoundJournal) WithOperation(ctx context.Context, id string, run func(*SwitchProgress, func(SwitchProgress) error) error) error {
	return j.FileJournal.WithOperation(ctx, id, func(current *SwitchProgress, save func(SwitchProgress) error) error {
		dir, _, err := j.openDir()
		if err != nil {
			return err
		}
		defer dir.Close()
		name := fmt.Sprintf("%x.config", sha256.Sum256([]byte(id)))
		f, err := journalFile(dir, name, unix.O_RDONLY)
		bound := err == nil
		if bound {
			value, rerr := readLimited(f, 64)
			f.Close()
			if rerr != nil || string(value) != j.confirmation {
				return fmt.Errorf("%w: configuration differs from the journaled operation", ErrSwitchConflict)
			}
		} else if !errors.Is(err, unix.ENOENT) || current != nil {
			return fmt.Errorf("%w: cannot verify the journaled configuration binding", ErrSwitchConflict)
		}
		return run(current, func(p SwitchProgress) error {
			if !bound {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := persistSwitchBinding(dir, name, j.confirmation); err != nil {
					return err
				}
				bound = true
			}
			return save(p)
		})
	})
}

func persistSwitchBinding(dir *os.File, name, value string) error {
	temp := ".config-" + rand.Text()
	f, err := journalFile(dir, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return fmt.Errorf("create configuration binding: %w", err)
	}
	fd := int(dir.Fd())
	defer unix.Unlinkat(fd, temp, 0)
	_, err = io.WriteString(f, value)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return fmt.Errorf("persist configuration binding: %w", err)
	}
	if err = unix.Renameat(fd, temp, fd, name); err != nil {
		return fmt.Errorf("publish configuration binding: %w", err)
	}
	return dir.Sync()
}
