// Command shimctl is the operator-facing companion CLI to `shim`.
// It generates per-(frontend, service) endpoint-override snippets
// so an end user can wire any cloud's official SDK / CLI / Terraform
// provider through a running shim.
//
// Usage:
//
//	shimctl env --frontend=<cloud> --service=<service> --endpoint=<shim-url> [--format=shell|tf]
//	shimctl list
//
// `shimctl env --format=shell` emits POSIX `export` lines suitable for
// `eval`. `--format=tf` emits the per-provider HCL endpoints-block
// snippet. Defaults to shell.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/e6qu/shimanism/internal/clientconfig"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "env":
		if err := runEnv(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shimctl env:", err)
			os.Exit(1)
		}
	case "list":
		if err := runList(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "shimctl list:", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintln(os.Stderr, "shimctl: unknown subcommand:", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: shimctl <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "  env  --frontend=<cloud> --service=<service> --endpoint=<url> [--format=shell|tf]")
	fmt.Fprintln(os.Stderr, "  list                                              List supported (frontend, service) pairs.")
}

func runEnv(args []string) error {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	frontend := fs.String("frontend", "", "Frontend cloud: aws, gcp, azure")
	service := fs.String("service", "", "Shimmed service: storage, secrets, queue, pubsub, rdbms, cache, functions, apigateway")
	endpoint := fs.String("endpoint", "", "Shim base URL (e.g. http://localhost:9000)")
	format := fs.String("format", "shell", "Output format: shell | tf")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *frontend == "" || *service == "" || *endpoint == "" {
		return fmt.Errorf("--frontend, --service, --endpoint are all required")
	}
	cfg, err := clientconfig.Load()
	if err != nil {
		return err
	}
	svc, err := cfg.Lookup(*frontend, *service)
	if err != nil {
		return err
	}
	switch *format {
	case "shell":
		out := svc.RenderShell(*endpoint)
		if out == "" {
			fmt.Fprintf(os.Stderr, "# no shell env vars defined for (%s, %s) — see `shimctl env --format=tf` or the SDK/CLI snippets below\n",
				*frontend, *service)
		}
		fmt.Print(out)
		if svc.CLI != "" {
			cli := strings.ReplaceAll(svc.CLI, "$SHIM", *endpoint)
			fmt.Fprintf(os.Stderr, "# CLI hint: %s\n", cli)
		}
		if svc.SDK != "" {
			sdk := strings.ReplaceAll(svc.SDK, "$SHIM", *endpoint)
			fmt.Fprintf(os.Stderr, "# SDK hint: %s\n", sdk)
		}
	case "tf":
		out := svc.RenderTerraform(*endpoint)
		if out == "" {
			return fmt.Errorf("(%s, %s) has no terraform endpoints block — check overrides.yaml", *frontend, *service)
		}
		if svc.Terraform.Provider != "" {
			fmt.Printf("provider %q {\n%s}\n", svc.Terraform.Provider, indent(out))
		} else {
			fmt.Print(out)
		}
	default:
		return fmt.Errorf("unknown --format %q (valid: shell, tf)", *format)
	}
	return nil
}

func runList(_ []string) error {
	cfg, err := clientconfig.Load()
	if err != nil {
		return err
	}
	for _, fe := range cfg.FrontendList() {
		fmt.Printf("%s:\n", fe)
		for _, s := range cfg.Services(fe) {
			fmt.Printf("  - %s\n", s)
		}
	}
	return nil
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}
