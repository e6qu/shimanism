// Command codegen reads a Smithy 2.0 JSON model and emits Go source for
// shimanism's HTTP handlers + request/response types for a specified set
// of operations.
//
// Usage:
//
//	codegen -spec=<path> -out=<file> -pkg=<package> -ops=<Op1,Op2>
//
// Example (used by `make codegen` in Phase 1.3):
//
//	codegen \
//	  -spec=services/storage/spec/aws-s3.smithy.json \
//	  -out=services/storage/gen/aws_s3.gen.go \
//	  -pkg=gen \
//	  -ops=ListBuckets
//
// The output is gofmt-formatted. Re-running the command must be
// deterministic: same input produces byte-identical output.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/e6qu/shimanism/internal/codegen/emit"
	"github.com/e6qu/shimanism/internal/codegen/smithy"
)

func main() {
	var (
		specPath = flag.String("spec", "", "path to the Smithy 2.0 JSON model")
		outPath  = flag.String("out", "", "path to the generated .gen.go file")
		pkgName  = flag.String("pkg", "gen", "Go package name for the generated file")
		opsList  = flag.String("ops", "", "comma-separated list of operation short names to emit (mutually exclusive with -all)")
		allOps   = flag.Bool("all", false, "emit every operation declared in the spec, in sorted order")
		commit   = flag.String("commit", "", "optional upstream commit SHA to record in the file header")
	)
	flag.Parse()

	if *specPath == "" || *outPath == "" {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "\n-spec and -out are required")
		os.Exit(2)
	}
	if (*opsList == "" && !*allOps) || (*opsList != "" && *allOps) {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "\nexactly one of -ops or -all is required")
		os.Exit(2)
	}

	data, err := os.ReadFile(*specPath)
	if err != nil {
		fail("read %s: %v", *specPath, err)
	}
	model, err := smithy.Parse(data)
	if err != nil {
		fail("parse %s: %v", *specPath, err)
	}

	var ops []string
	if *allOps {
		opIDs := model.AllOperations()
		ops = make([]string, len(opIDs))
		for i, id := range opIDs {
			ops[i] = smithy.ShortName(id)
		}
		sort.Strings(ops)
	} else {
		ops = strings.Split(*opsList, ",")
		for i, o := range ops {
			ops[i] = strings.TrimSpace(o)
		}
	}

	src, err := emit.Emit(model, emit.Options{
		PackageName:  *pkgName,
		SourceFile:   *specPath,
		SourceCommit: *commit,
		Operations:   ops,
	})
	if err != nil {
		fail("emit: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fail("mkdir %s: %v", filepath.Dir(*outPath), err)
	}
	if err := os.WriteFile(*outPath, src, 0o644); err != nil {
		fail("write %s: %v", *outPath, err)
	}

	fmt.Printf("wrote %s (%d bytes, %d operation(s))\n", *outPath, len(src), len(ops))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "codegen: "+format+"\n", args...)
	os.Exit(1)
}
