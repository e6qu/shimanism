package main

import (
	"reflect"
	"regexp"
	"testing"
)

// TestTemplateToRegex_VarSyntax exercises the URI-template compiler
// across the cases the emitter actually emits in production gen
// files: literal segments, single-segment `{var}`, reserved-
// expansion `{+var}`, multiple vars in one template.
func TestTemplateToRegex_VarSyntax(t *testing.T) {
	cases := []struct {
		name    string
		tmpl    string
		want    string
		vars    []string
		samples map[string]bool // path → expected match
	}{
		{
			name: "literal-only",
			tmpl: "v1/buckets",
			want: `^/?v1/buckets$`,
			vars: nil,
			samples: map[string]bool{
				"v1/buckets":  true,
				"/v1/buckets": true,
				"v1/objects":  false,
			},
		},
		{
			name: "single-segment-var",
			tmpl: "b/{bucket}",
			want: `^/?b/([^/]+)$`,
			vars: []string{"bucket"},
			samples: map[string]bool{
				"b/my-bucket": true,
				// slash in capture should fail (single-segment)
				"b/my/bucket": false,
			},
		},
		{
			name: "reserved-expansion-var",
			tmpl: "v1/{+name}",
			want: `^/?v1/(.+)$`,
			vars: []string{"name"},
			samples: map[string]bool{
				"v1/projects/p/secrets/s":          true,
				"v1/projects/p/secrets/s/versions": true,
				"v2/projects/p/secrets/s":          false, // literal mismatch
			},
		},
		{
			name: "multi-var",
			tmpl: "b/{bucket}/o/{object}",
			want: `^/?b/([^/]+)/o/([^/]+)$`,
			vars: []string{"bucket", "object"},
			samples: map[string]bool{
				"b/mb/o/mo":           true,
				"b/mb/o/path%2Fto%2F": true,
			},
		},
		{
			name: "regex-meta-in-literal",
			// Discovery emits paths like `:access` with a literal
			// colon — fine. Dots aren't typical but `.` would be
			// a regex meta if we forgot to escape.
			tmpl: "v1/{+name}:access",
			want: `^/?v1/(.+):access$`,
			vars: []string{"name"},
			samples: map[string]bool{
				"v1/projects/p/secrets/s/versions/1:access": true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRe, gotVars := templateToRegex(tc.tmpl)
			if gotRe != tc.want {
				t.Errorf("regex source mismatch:\n  got  %s\n  want %s", gotRe, tc.want)
			}
			if !reflect.DeepEqual(gotVars, tc.vars) {
				t.Errorf("vars mismatch: got %v, want %v", gotVars, tc.vars)
			}
			re := regexp.MustCompile(gotRe)
			for path, wantMatch := range tc.samples {
				gotMatch := re.MatchString(path)
				if gotMatch != wantMatch {
					t.Errorf("sample %q: match = %v, want %v", path, gotMatch, wantMatch)
				}
			}
		})
	}
}
