package cutover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lkhmm520/portloom/internal/domain"
)

type planConfig struct {
	Profiles map[string]profile      `json:"profiles"`
	Groups   map[string]mappingGroup `json:"groups"`
}

type mappingGroup struct {
	Hosts []string `json:"hosts"`
}

func loadConfig(path string) (planConfig, error) {
	data, err := readLocal(path, configLimit, false)
	if err != nil {
		return planConfig{}, fmt.Errorf("%w: could not read profiles config", ErrInput)
	}
	var config planConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Profiles) == 0 {
		return planConfig{}, fmt.Errorf("%w: expected a JSON config with named profiles and optional groups", ErrInput)
	}
	if !uniqueConfigKeys(json.NewDecoder(bytes.NewReader(data))) {
		return planConfig{}, fmt.Errorf("%w: duplicate JSON keys in profiles config", ErrInput)
	}
	// Sort validation order for stable errors when several definitions are invalid.
	names := make([]string, 0, len(config.Groups))
	for name := range config.Groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !domain.ValidAgentName(name) {
			return planConfig{}, fmt.Errorf("%w: invalid group name in profiles config", ErrInput)
		}
		hosts, err := normalizeHosts(config.Groups[name].Hosts)
		if err != nil {
			return planConfig{}, fmt.Errorf("group hosts: %w", err)
		}
		config.Groups[name] = mappingGroup{Hosts: hosts}
	}
	return config, nil
}

// The standard decoder silently accepts duplicate keys and matches struct
// fields case-insensitively. Reject overlapping definitions before planning.
// This walks already validated, size-bounded JSON without echoing its contents.
func uniqueConfigKeys(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return false
			}
			name := strings.ToUpper(key.(string))
			if seen[name] || !uniqueConfigKeys(decoder) {
				return false
			}
			seen[name] = true
		}
	case json.Delim('['):
		for decoder.More() {
			if !uniqueConfigKeys(decoder) {
				return false
			}
		}
	default:
		return true
	}
	_, err = decoder.Token()
	return err == nil
}

func normalizeHosts(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: select at least one exact DNS hostname", ErrInput)
	}
	set := make(map[string]bool, len(values))
	hosts := make([]string, 0, len(values))
	for _, value := range values {
		host, ok := domain.NormalizeDNSHost(value)
		if !ok {
			return nil, fmt.Errorf("%w: expected an exact DNS hostname", ErrInput)
		}
		if !set[host] {
			set[host] = true
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts, nil
}
