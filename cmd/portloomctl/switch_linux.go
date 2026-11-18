package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/lkhmm520/portloom/internal/cutover"
)

const operationalUsage = `
Linux cutover (see cmd/portloomctl/README.md for config and prerequisites):
  portloomctl check --config FILE --operation ID
  portloomctl status --journal-dir DIR --operation ID
  portloomctl switch --config FILE --operation ID --confirm SHA256
  portloomctl settle --config FILE --operation ID --confirm SHA256
  portloomctl rollback --config FILE --operation ID --confirm SHA256

check validates local configuration and prints its confirmation digest; it
is not a live preflight. status reads local journal progress, not live state.
switch starts or resumes forward; settle applies only the recorded pending
write; rollback never sends a pending forward write. A nonzero exit does not
mean no writes occurred. Inspect the journal before deciding how to recover.
`

func isOperational(verb string) bool {
	switch verb {
	case "check", "status", "switch", "settle", "rollback":
		return true
	}
	return false
}

func runOperational(ctx context.Context, args []string, out, stderr io.Writer) int {
	verb := args[0]
	flags := flag.NewFlagSet(verb, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("operation", "", "")
	var config, dir, confirm string
	if verb == "status" {
		flags.StringVar(&dir, "journal-dir", "", "")
	} else {
		flags.StringVar(&config, "config", "", "")
	}
	mutation := verb != "status" && verb != "check"
	if mutation {
		flags.StringVar(&confirm, "confirm", "", "")
	}
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(out, operationalUsage)
		return 0
	} else if err != nil || flags.NArg() != 0 || *id == "" || (verb == "status" && dir == "") || (verb != "status" && config == "") {
		fmt.Fprint(stderr, operationalUsage)
		return 2
	}
	if mutation {
		if decoded, err := hex.DecodeString(confirm); err != nil || len(decoded) != 32 {
			fmt.Fprintln(stderr, "portloomctl: a SHA-256 confirmation from check is required")
			return 2
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var progress cutover.SwitchProgress
	var err error
	if verb == "status" {
		progress, err = cutover.ReadSwitchStatus(ctx, dir, *id)
	} else {
		setup, err := cutover.OpenSwitchSetup(config, *id)
		if err != nil {
			return operationalError(stderr, err)
		}
		defer setup.Close()
		if verb == "check" {
			return operationalJSON(out, stderr, struct {
				Operation    string   `json:"operation_id"`
				Source       string   `json:"source"`
				Target       string   `json:"target"`
				Hosts        []string `json:"hosts"`
				Confirmation string   `json:"confirmation"`
			}{*id, setup.Spec.Source.ID, setup.Spec.Target.ID, setup.Spec.Hosts, setup.Confirmation})
		}
		progress, err = setup.Execute(ctx, verb, confirm)
		if err != nil {
			// Progress may contain the last barrier, not the outcome of a write whose
			// response or journal sync was lost. The journal is the recovery entry point.
			if progress.Status != "" || !errors.Is(err, cutover.ErrInput) {
				fmt.Fprintln(stderr, "portloomctl: inspect local status before retrying; writes may have occurred")
			}
			return operationalError(stderr, err)
		}
	}
	if err != nil {
		return operationalError(stderr, err)
	}
	return operationalJSON(out, stderr, struct {
		Operation         string               `json:"operation_id"`
		Status            cutover.SwitchStatus `json:"status"`
		Pending           cutover.SwitchAction `json:"pending,omitempty"`
		Source            string               `json:"source"`
		Target            string               `json:"target"`
		Hosts             []string             `json:"hosts"`
		TrafficGeneration int64                `json:"traffic_generation"`
	}{*id, progress.Status, progress.Pending, progress.Spec.Source.ID, progress.Spec.Target.ID, progress.Spec.Hosts, progress.Traffic.Generation})
}

func operationalJSON(out, stderr io.Writer, value any) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		fmt.Fprintln(stderr, "portloomctl: could not write report")
		return 1
	}
	return 0
}

func operationalError(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "portloomctl:", err)
	if errors.Is(err, cutover.ErrInput) {
		return 2
	}
	return 1
}
