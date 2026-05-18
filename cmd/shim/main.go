// Command shim is the entry point for the shimanism protocol-translation
// proxy. At Phase 1.1 this is a placeholder that prints version and exits;
// Phase 1.5+ wires up actual handlers per service.
package main

import (
	"fmt"
	"os"
)

const version = "0.0.0-phase-1.1"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(version)
		return
	}
	fmt.Fprintf(os.Stderr, "shim %s — no service handlers registered yet (Phase 1.1 placeholder)\n", version)
	fmt.Fprintf(os.Stderr, "See PLAN.md for the roadmap.\n")
	os.Exit(2)
}
