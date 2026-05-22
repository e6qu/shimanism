package main

import (
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
