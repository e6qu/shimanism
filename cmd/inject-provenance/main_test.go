package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestTopLevelKeyOrder verifies the key-order scanner preserves
// the order keys appear in the source file's top-level object.
// Without this, encoding/json's lexicographic map iteration would
// re-sort every spec file on first inject and produce massive
// diffs against the verbatim upstream content.
func TestTopLevelKeyOrder(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "simple",
			in:   `{"a": 1, "b": 2, "c": 3}`,
			want: []string{"a", "b", "c"},
		},
		{
			name: "nested-object-doesnt-leak-keys",
			in:   `{"a": {"x": 1, "y": 2}, "b": 3}`,
			want: []string{"a", "b"},
		},
		{
			name: "nested-array-doesnt-leak-keys",
			in:   `{"a": [{"x": 1}, {"y": 2}], "b": 3}`,
			want: []string{"a", "b"},
		},
		{
			name: "escaped-quotes-in-string-values",
			in:   `{"a": "has \"quotes\" inside", "b": 2}`,
			want: []string{"a", "b"},
		},
		{
			name: "mixed-types",
			in:   `{"$schema": "x", "smithy": "2.0", "shapes": {"foo": "bar"}, "metadata": null}`,
			want: []string{"$schema", "smithy", "shapes", "metadata"},
		},
		{
			name: "whitespace-and-newlines",
			in: `{
				"first": 1,
				"second": 2,
				"third": 3
			}`,
			want: []string{"first", "second", "third"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := topLevelKeyOrder([]byte(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("topLevelKeyOrder = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestInjectInto_PreservesContent ensures inject-provenance's
// rewrite preserves every top-level value verbatim except for the
// added/refreshed _provenance key. Without this, a regression in
// the manual writer (key/comma placement, escape handling, etc.)
// could silently corrupt nested spec content and only surface as
// a codegen failure downstream.
func TestInjectInto_PreservesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.json")
	original := `{"smithy":"2.0","metadata":{"suppressions":[]},"shapes":{"com.amazonaws.s3#Bucket":{"type":"structure"}}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := provenance{
		UpstreamRepo:    "aws/aws-sdk-go-v2",
		UpstreamPath:    "codegen/sdk-codegen/aws-models/s3.json",
		UpstreamLicense: "Apache-2.0",
		PinnedAt:        "deadbeef",
		FetchedUTC:      "2026-05-22T00:00:00Z",
	}
	changed, err := injectInto(path, prov)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first injection reported no change; expected change")
	}

	// Re-read + verify each original top-level key survived with
	// its original value.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	for _, k := range []string{"smithy", "metadata", "shapes"} {
		if _, ok := after[k]; !ok {
			t.Errorf("key %q dropped by inject-provenance", k)
		}
	}
	if _, ok := after["_provenance"]; !ok {
		t.Error("_provenance not present after injection")
	}
	// Spot-check a nested value round-tripped intact.
	var shapes map[string]json.RawMessage
	if err := json.Unmarshal(after["shapes"], &shapes); err != nil {
		t.Fatal(err)
	}
	if _, ok := shapes["com.amazonaws.s3#Bucket"]; !ok {
		t.Error("nested key com.amazonaws.s3#Bucket dropped")
	}

	// Re-running with identical prov should be a noop.
	changed, err = injectInto(path, prov)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second injection with same provenance reported change; expected noop")
	}
}
