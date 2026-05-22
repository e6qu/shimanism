package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryVendoredSpecCarriesProvenance walks `services/*/spec/*.json`
// + the common-types tree, asserting each JSON file has `_provenance`
// as its first top-level key. Guards against a contributor refreshing
// a spec by hand (curl + commit) without re-running inject-provenance —
// the spec would lose self-documentation and SOURCES.md would drift
// out of sync.
func TestEveryVendoredSpecCarriesProvenance(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	var specs []string
	for _, root := range []string{
		filepath.Join(repoRoot, "services"),
	} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".json") {
				return nil
			}
			// Manifests + codegen.json files live under services/<svc>/
			// alongside the spec/ subdir, but they're not vendored
			// specs — skip them.
			rel, _ := filepath.Rel(repoRoot, path)
			if !strings.Contains(rel, "/spec/") && !strings.Contains(rel, "/common-types/") {
				return nil
			}
			// Discovery + ARM + Smithy specs are vendored; codegen
			// manifests live one level up and don't match.
			specs = append(specs, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(specs) == 0 {
		t.Fatal("found zero vendored specs — walk pattern is wrong")
	}

	missing := []string{}
	for _, path := range specs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			// common-types files include relative-path $refs, etc.;
			// they're valid JSON so unmarshal should succeed.
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		if _, ok := doc["_provenance"]; !ok {
			rel, _ := filepath.Rel(repoRoot, path)
			missing = append(missing, rel)
			continue
		}
		// Also verify it's the FIRST key (the contract inject-
		// provenance enforces so the provenance is visible at the
		// top when the file is opened).
		order := topLevelKeyOrder(raw)
		if len(order) > 0 && order[0] != "_provenance" {
			rel, _ := filepath.Rel(repoRoot, path)
			t.Errorf("%s: _provenance is not the first top-level key (first is %q)", rel, order[0])
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d vendored spec(s) missing `_provenance` key:\n  %s\nFix: scripts/fetch-*.sh re-runs cmd/inject-provenance after download.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestProvenanceMatchesSOURCES walks every SOURCES.md, parses each
// row, then asserts the matching spec file's `_provenance` block
// has the same upstream_repo + upstream_path + pinned_at. Catches
// the case where a contributor edits SOURCES.md (bumps the SHA,
// say) but forgets to re-run `make inject-provenance` — the spec
// file would silently disagree with SOURCES.md downstream.
func TestProvenanceMatchesSOURCES(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	var sourcesFiles []string
	err = filepath.WalkDir(filepath.Join(repoRoot, "services"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == "SOURCES.md" {
			sourcesFiles = append(sourcesFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	mismatches := 0
	for _, sourcesPath := range sourcesFiles {
		rows, err := parseSourcesMD(sourcesPath)
		if err != nil {
			t.Errorf("parse %s: %v", sourcesPath, err)
			continue
		}
		dir := filepath.Dir(sourcesPath)
		for local, want := range rows {
			specPath := filepath.Join(dir, local)
			if _, err := os.Stat(specPath); err != nil {
				continue // missing files reported by the other test
			}
			raw, err := os.ReadFile(specPath)
			if err != nil {
				t.Errorf("read %s: %v", specPath, err)
				continue
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(raw, &doc); err != nil {
				continue
			}
			provRaw, ok := doc["_provenance"]
			if !ok {
				continue
			}
			var got provenance
			if err := json.Unmarshal(provRaw, &got); err != nil {
				t.Errorf("%s: decode _provenance: %v", specPath, err)
				continue
			}
			rel, _ := filepath.Rel(repoRoot, specPath)
			if got.UpstreamRepo != want.UpstreamRepo {
				t.Errorf("%s: _provenance.upstream_repo = %q, SOURCES.md = %q", rel, got.UpstreamRepo, want.UpstreamRepo)
				mismatches++
			}
			if got.UpstreamPath != want.UpstreamPath {
				t.Errorf("%s: _provenance.upstream_path = %q, SOURCES.md = %q", rel, got.UpstreamPath, want.UpstreamPath)
				mismatches++
			}
			if got.PinnedAt != want.PinnedAt {
				t.Errorf("%s: _provenance.pinned_at = %q, SOURCES.md = %q", rel, got.PinnedAt, want.PinnedAt)
				mismatches++
			}
		}
	}
	if mismatches > 0 {
		t.Logf("Fix: run `make inject-provenance` to sync _provenance blocks with SOURCES.md.")
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
