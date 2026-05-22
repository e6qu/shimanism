package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestGenHeadersCarryProvenance asserts every .gen.go file under
// services/ carries an upstream-provenance line in its header:
//
//   - Azure (`services/*/gen/azure/*.gen.go`): `Upstream commit: <40-hex>`
//   - AWS   (`services/*/gen/*.gen.go`, not under gen/{azure,gcp}/): same
//   - GCP   (`services/*/gen/gcp/*.gen.go`): `Discovery revision: <digits>`
//
// The Makefile wires these through from each spec's SOURCES.md (or
// from the Discovery JSON for GCP). When the wiring breaks (wrong
// dir grepped, missing row, mis-parsed), gen files emit a
// placeholder value and the audit trail tying gen output to a
// vendored spec snapshot vanishes silently. This test fails fast
// instead.
func TestGenHeadersCarryProvenance(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	commitRe := regexp.MustCompile(`Upstream commit: ([0-9a-f]{40})`)
	revisionRe := regexp.MustCompile(`Discovery revision: ([0-9]+)`)

	var azure, aws, gcp int
	err = filepath.WalkDir(filepath.Join(repoRoot, "services"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".gen.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		head := raw
		if len(head) > 1024 {
			head = head[:1024]
		}
		rel, _ := filepath.Rel(repoRoot, path)
		switch {
		case strings.Contains(path, "/gen/azure/"):
			azure++
			m := commitRe.FindStringSubmatch(string(head))
			if m == nil {
				t.Errorf("%s: missing `Upstream commit: <40-hex>` line", rel)
				return nil
			}
			if m[1] == strings.Repeat("0", 40) {
				t.Errorf("%s: placeholder zero SHA; SOURCES.md row missing or mis-parsed", rel)
			}
		case strings.Contains(path, "/gen/gcp/"):
			gcp++
			if !revisionRe.MatchString(string(head)) {
				t.Errorf("%s: missing `Discovery revision: <digits>` line", rel)
			}
		default:
			// AWS Smithy lane: lives directly under services/<svc>/gen/.
			aws++
			m := commitRe.FindStringSubmatch(string(head))
			if m == nil {
				t.Errorf("%s: missing `Upstream commit: <40-hex>` line", rel)
				return nil
			}
			if m[1] == strings.Repeat("0", 40) {
				t.Errorf("%s: placeholder zero SHA; SOURCES.md row missing or mis-parsed", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if azure == 0 || aws == 0 || gcp == 0 {
		t.Errorf("expected ≥1 gen file per lane; got azure=%d aws=%d gcp=%d", azure, aws, gcp)
	}
}
