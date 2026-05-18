// Package restxml is the runtime support library that shimanism's
// generated REST-XML handlers (AWS S3, Route 53, CloudFront, etc.)
// call into. It is hand-written, not generated.
//
// The package is deliberately small: URI template matching, header /
// query parsing, time-format helpers, and the cloud-vocabulary error
// envelope. Everything beyond that lives in the backend implementation.
package restxml

import (
	"fmt"
	"net/url"
	"strings"
)

// MatchURI compares an incoming URL path against a Smithy URI template
// (e.g. "/{Bucket}/{Key+}") and returns the extracted label values.
// The template's literal segments must match exactly; label segments
// (`{name}`) capture a single non-slash segment; greedy labels
// (`{name+}`) capture the entire remaining path including slashes.
//
// Returns (labels, true) on a successful match. Returns (nil, false)
// when the template does not match.
//
// Any "?query" suffix on the template is ignored — query bindings are
// extracted separately via url.URL.Query.
func MatchURI(path, template string) (map[string]string, bool) {
	if i := strings.IndexByte(template, '?'); i >= 0 {
		template = template[:i]
	}
	labels := map[string]string{}

	// Walk both strings in lockstep. Templates and paths both start with
	// '/' for AWS REST URIs; treat them as starting from index 1 (after
	// the leading slash) and segment-by-segment.
	tparts := splitURIPath(template)
	pparts := splitURIPath(path)

	ti, pi := 0, 0
	for ti < len(tparts) {
		t := tparts[ti]
		if !isLabel(t) {
			// Literal segment.
			if pi >= len(pparts) || pparts[pi] != t {
				return nil, false
			}
			ti++
			pi++
			continue
		}
		name, greedy := labelName(t)
		if greedy {
			// Greedy labels consume the rest of the path. Anything after
			// the greedy label in the template is a static suffix and is
			// rare in practice; we don't support that pattern.
			if ti+1 != len(tparts) {
				return nil, false
			}
			rest := strings.Join(pparts[pi:], "/")
			decoded, err := url.PathUnescape(rest)
			if err != nil {
				return nil, false
			}
			labels[name] = decoded
			return labels, true
		}
		// Non-greedy label: consume exactly one segment.
		if pi >= len(pparts) {
			return nil, false
		}
		decoded, err := url.PathUnescape(pparts[pi])
		if err != nil {
			return nil, false
		}
		labels[name] = decoded
		ti++
		pi++
	}
	if pi != len(pparts) {
		return nil, false
	}
	return labels, true
}

func splitURIPath(s string) []string {
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func isLabel(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}

// labelName returns ("Foo", false) for "{Foo}" and ("Bar", true) for "{Bar+}".
func labelName(seg string) (string, bool) {
	inner := seg[1 : len(seg)-1]
	if strings.HasSuffix(inner, "+") {
		return inner[:len(inner)-1], true
	}
	return inner, false
}

// URITemplateError is returned to a handler when a request path fails
// the URI-template match for the operation's mounted route.
type URITemplateError struct {
	Path     string
	Template string
}

func (e *URITemplateError) Error() string {
	return fmt.Sprintf("restxml: path %q does not match URI template %q", e.Path, e.Template)
}
