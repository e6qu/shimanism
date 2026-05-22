// Command inject-provenance reads a services/<svc>/spec/SOURCES.md
// table and rewrites every referenced JSON file to carry a top-level
// `_provenance` key documenting its upstream repo + path + pinned
// SHA (or Discovery revision) + license + fetched timestamp.
//
// JSON has no comment syntax; this is the closest analogue. The
// codegen tools tolerate unknown top-level keys (they iterate known
// fields by name), so `_provenance` is a benign decoration.
//
// `_provenance` is always written as the FIRST key of the resulting
// JSON object so it's visible at the top when the file is opened in
// an editor or viewed in a code-review diff.
//
// Re-running is idempotent: existing `_provenance` keys are replaced
// with the row's current values. SOURCES.md is the authoritative
// store; the spec file's `_provenance` is a derived projection.
//
// Usage:
//
//	inject-provenance -sources=services/storage/spec/SOURCES.md -dir=services/storage/spec
//
// To process every service's spec dir at once, pair with `find`:
//
//	find services -name SOURCES.md -execdir sh -c 'inject-provenance -sources=SOURCES.md -dir=.' \;
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type provenance struct {
	UpstreamRepo    string `json:"upstream_repo"`
	UpstreamPath    string `json:"upstream_path"`
	UpstreamLicense string `json:"upstream_license"`
	PinnedAt        string `json:"pinned_at"`
	FetchedUTC      string `json:"fetched_utc"`
	Note            string `json:"note,omitempty"`
}

// backtickedRe extracts the first backticked span from a table cell.
// Cells may carry extra prose alongside the value (e.g. the Pinned
// column says "revision `20260516`" or the Upstream-path column adds
// "(live Discovery document)" after the backtick).
var backtickedRe = regexp.MustCompile("`([^`]+)`")

func main() {
	sources := flag.String("sources", "", "path to a services/<svc>/spec/SOURCES.md")
	dir := flag.String("dir", "", "directory containing the JSON spec files referenced by SOURCES.md")
	flag.Parse()
	if *sources == "" || *dir == "" {
		flag.Usage()
		os.Exit(2)
	}

	rows, err := parseSourcesMD(*sources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", *sources, err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Fprintf(os.Stderr, "no rows parsed from %s\n", *sources)
		os.Exit(1)
	}

	updated := 0
	for local, prov := range rows {
		path := filepath.Join(*dir, local)
		// SOURCES.md rows may reference subpaths
		// (e.g. "resource-management/v4/types.json"); allow.
		if _, err := os.Stat(path); err != nil {
			fmt.Printf("skip %s (not found)\n", path)
			continue
		}
		changed, err := injectInto(path, prov)
		if err != nil {
			fmt.Fprintf(os.Stderr, "inject %s: %v\n", path, err)
			os.Exit(1)
		}
		if changed {
			updated++
			fmt.Printf("ok   %s\n", path)
		} else {
			fmt.Printf("noop %s (already current)\n", path)
		}
	}
	fmt.Printf("\nwrote %d/%d files\n", updated, len(rows))
}

func parseSourcesMD(path string) (map[string]provenance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]provenance{}
	for _, line := range strings.Split(string(data), "\n") {
		// Skip non-table lines + header / separator.
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		// Split on `|`. Leading + trailing empties from outer `|`.
		cells := strings.Split(line, "|")
		if len(cells) < 7 { // outer `|` produces empty cells at index 0 and last
			continue
		}
		cells = cells[1 : len(cells)-1] // strip outer empties
		if len(cells) != 6 {
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		// Header row: cells like "Local file", no backticks.
		// Separator row: cells like "---".
		if cells[0] == "Local file" || strings.HasPrefix(cells[0], "---") || strings.HasPrefix(cells[0], ":") {
			continue
		}
		// Extract the first backticked span from each cell. The
		// license column has no backticks; copy verbatim.
		extract := func(cell string) string {
			if m := backtickedRe.FindStringSubmatch(cell); m != nil {
				return m[1]
			}
			return strings.TrimSpace(cell)
		}
		local := extract(cells[0])
		if local == "" || strings.HasPrefix(local, "Local") {
			continue
		}
		out[local] = provenance{
			UpstreamRepo:    extract(cells[1]),
			UpstreamPath:    extract(cells[2]),
			UpstreamLicense: cells[3],
			PinnedAt:        extract(cells[4]),
			FetchedUTC:      cells[5],
			Note:            "Vendored verbatim except for this top-level `_provenance` key. Refresh with the matching scripts/fetch-*.sh; see SOURCES.md for the authoritative pin.",
		}
	}
	return out, nil
}

func injectInto(path string, prov provenance) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// Use json.Decoder with UseNumber so integer/float precision
	// round-trips losslessly. Most spec files are huge; we must
	// preserve every field.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		return false, fmt.Errorf("decode JSON: %w", err)
	}

	provBytes, err := json.MarshalIndent(prov, "  ", "  ")
	if err != nil {
		return false, err
	}
	provRaw := json.RawMessage(provBytes)

	// Check if existing _provenance is byte-identical to what we'd write.
	if existing, ok := doc["_provenance"]; ok {
		if bytes.Equal(existing, provRaw) {
			return false, nil
		}
	}

	// Build an ordered output: _provenance first, then every other
	// key in the order it appeared in the source file. encoding/json
	// emits map keys in lexicographic order, so we can't rely on it
	// for ordering. Use a manual writer.
	var ordered []string
	seen := map[string]bool{"_provenance": true}
	keyOrder := topLevelKeyOrder(raw)
	ordered = append(ordered, "_provenance")
	for _, k := range keyOrder {
		if k == "_provenance" {
			continue
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		ordered = append(ordered, k)
	}

	var out bytes.Buffer
	out.WriteString("{\n")
	for i, k := range ordered {
		out.WriteString("  ")
		keyJSON, _ := json.Marshal(k)
		out.Write(keyJSON)
		out.WriteString(": ")
		var v json.RawMessage
		if k == "_provenance" {
			v = provRaw
		} else {
			v = doc[k]
		}
		out.Write(v)
		if i < len(ordered)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")

	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// topLevelKeyOrder scans the raw JSON bytes and returns the order in
// which top-level keys appear. This is needed because Go's encoding/
// json map iteration is randomised and would lose the source-file
// ordering. The scan is intentionally simple: it doesn't validate
// the JSON, just tracks paren depth + extracts quoted keys at depth
// 1 immediately after `{` or `,`.
func topLevelKeyOrder(raw []byte) []string {
	var out []string
	depth := 0
	expectKey := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch c {
		case '{':
			depth++
			if depth == 1 {
				expectKey = true
			}
		case '}':
			depth--
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 1 {
				expectKey = true
			}
		case '"':
			if expectKey && depth == 1 {
				// Read until the matching closing quote.
				j := i + 1
				var key bytes.Buffer
				for j < len(raw) {
					if raw[j] == '\\' && j+1 < len(raw) {
						key.WriteByte(raw[j])
						key.WriteByte(raw[j+1])
						j += 2
						continue
					}
					if raw[j] == '"' {
						break
					}
					key.WriteByte(raw[j])
					j++
				}
				out = append(out, key.String())
				expectKey = false
				i = j
			} else {
				// Skip string literal.
				j := i + 1
				for j < len(raw) {
					if raw[j] == '\\' && j+1 < len(raw) {
						j += 2
						continue
					}
					if raw[j] == '"' {
						break
					}
					j++
				}
				i = j
			}
		}
	}
	return out
}
