package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/cutover"
)

const usage = `Usage: portloomctl plan --config FILE --source PROFILE --target PROFILE
       --source-client ID --target-client ID (--group NAME | --host DOMAIN [--host DOMAIN ...])

Read-only HTTPS preflight of whole, exact business hostnames. Only main-port
HTTPS routes are supported; disabled rows are included. Output is JSON, not
an authorization to execute. No SSH, data-plane TLS, public or DNS checks.

Config: {"profiles":{"NAME":{"url":"https://api.example.com",
         "management_host":"console.example.com","token_file":"NAME.token",
         "ca_file":"optional-private-ca.pem"}},
         "groups":{"NAME":{"hosts":["media.example.com"]}}}
Groups are optional reusable host sets; profiles and client IDs are explicit.
Paths are relative to the config file (or absolute). Token files must be private
regular files (e.g. chmod 600); omit ca_file to use system trust only.

Exit codes: 0 preview without inventory blockers; 1 blocked/unavailable;
            2 invalid invocation or local config.
`

type hostsFlag []string

func (h *hostsFlag) String() string         { return strings.Join(*h, ",") }
func (h *hostsFlag) Set(value string) error { *h = append(*h, value); return nil }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprint(out, usage, operationalUsage)
		return 0
	}
	if len(args) != 0 && isOperational(args[0]) {
		return runOperational(ctx, args, out, stderr)
	}
	if len(args) == 0 || args[0] != "plan" {
		fmt.Fprint(stderr, usage, operationalUsage)
		return 2
	}
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // flag parse errors can echo arguments; don't print them.
	config := flags.String("config", "", "")
	var req cutover.Request
	var hosts hostsFlag
	flags.StringVar(&req.Source, "source", "", "")
	flags.StringVar(&req.Target, "target", "", "")
	flags.StringVar(&req.SourceClient, "source-client", "", "")
	flags.StringVar(&req.TargetClient, "target-client", "", "")
	flags.Var(&hosts, "host", "")
	flags.Func("group", "", func(value string) error {
		if value == "" || req.Group != "" {
			return errors.New("select one nonempty group")
		}
		req.Group = value
		return nil
	})
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(out, usage)
		return 0
	} else if err != nil || flags.NArg() != 0 || *config == "" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	req.Hosts = hosts
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	report, err := cutover.Plan(ctx, *config, req)
	if err != nil {
		fmt.Fprintln(stderr, "portloomctl:", err)
		if errors.Is(err, cutover.ErrInput) {
			return 2
		}
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, "portloomctl: could not write report")
		return 1
	}
	if report.Status == "blocked" {
		return 1
	}
	return 0
}
