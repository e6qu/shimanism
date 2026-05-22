package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAzureGenHeadersCarryUpstreamCommit asserts every Azure .gen.go
// file under services/*/gen/azure/ has an "Upstream commit: <40-hex>"
// line in its header. The Makefile codegen target wires the commit
// through from SOURCES.md; if the wiring breaks (greps the wrong
// field, SOURCES.md row format changes), gen files would be emitted
// with an empty placeholder SHA and CI would silently lose the
// audit trail tying gen output to vendored spec snapshot.
func TestAzureGenHeadersCarryUpstreamCommit(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	commitRe := regexp.MustCompile(`Upstream commit: ([0-9a-f]{40})`)

	var found int
	err = filepath.WalkDir(filepath.Join(repoRoot, "services"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".gen.go") || !strings.Contains(path, "/gen/azure/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Read only the header — first 1KB is enough.
		head := raw
		if len(head) > 1024 {
			head = head[:1024]
		}
		m := commitRe.FindStringSubmatch(string(head))
		rel, _ := filepath.Rel(repoRoot, path)
		if m == nil {
			t.Errorf("%s: header missing `Upstream commit: <40-hex>` line", rel)
			return nil
		}
		// Reject the placeholder all-zeros SHA (the Makefile uses
		// it when SOURCES.md has no row for the spec).
		if m[1] == strings.Repeat("0", 40) {
			t.Errorf("%s: header has placeholder zero SHA; SOURCES.md row missing or mis-parsed", rel)
		}
		found++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if found == 0 {
		t.Fatal("no Azure gen files found — walk pattern is wrong")
	}
}
