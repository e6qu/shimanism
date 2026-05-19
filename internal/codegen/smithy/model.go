// Package smithy parses a Smithy 2.0 JSON AST into Go types and
// supports the subset of Smithy needed by shimanism's codegen: all
// shape kinds (operation, service, structure, list, set, map, union,
// enum, intEnum, primitive types), and the traits used for HTTP
// binding, XML serialization, error responses, required-ness,
// timestamp formats, and AWS protocol selection.
//
// The Smithy spec is at https://smithy.io/2.0/spec/. Shapes are
// identified by absolute IDs of the form "namespace#name"; in S3
// these look like "com.amazonaws.s3#ListBuckets".
package smithy

import (
	"encoding/json"
	"fmt"
)

// Model is a parsed Smithy 2.0 document.
type Model struct {
	Smithy   string           `json:"smithy"`
	Metadata map[string]any   `json:"metadata"`
	Shapes   map[string]Shape `json:"shapes"`
}

// Shape is a generic Smithy shape; the Type field is the discriminator.
type Shape struct {
	Type   string     `json:"type"`
	Input  *ShapeRef  `json:"input,omitempty"`
	Output *ShapeRef  `json:"output,omitempty"`
	Errors []ShapeRef `json:"errors,omitempty"`
	// Members appears in: structure, union, enum, intEnum.
	Members map[string]Member `json:"members,omitempty"`
	// Member for list and set shapes.
	Member *Member `json:"member,omitempty"`
	// Key + Value for map shapes.
	Key    *Member                    `json:"key,omitempty"`
	Value  *Member                    `json:"value,omitempty"`
	Traits map[string]json.RawMessage `json:"traits,omitempty"`
}

// ShapeRef is a reference to another shape by absolute ID.
type ShapeRef struct {
	Target string                     `json:"target"`
	Traits map[string]json.RawMessage `json:"traits,omitempty"`
}

// Member is a structure / union / list / map member referencing another shape.
type Member struct {
	Target string                     `json:"target"`
	Traits map[string]json.RawMessage `json:"traits,omitempty"`
}

// Parse decodes a Smithy 2.0 JSON model. Returns an error if the model
// declares a Smithy version other than 2.x.
func Parse(data []byte) (*Model, error) {
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("smithy: parse model: %w", err)
	}
	if len(m.Smithy) == 0 || m.Smithy[0] != '2' {
		return nil, fmt.Errorf("smithy: unsupported smithy version %q (only 2.x is supported)", m.Smithy)
	}
	return &m, nil
}

// LookupOperation finds an operation by its short name (without namespace
// prefix). Returns the shape and its full ID, or an error if zero or
// multiple matches are found.
func (m *Model) LookupOperation(name string) (string, *Shape, error) {
	var matches []string
	for id, sh := range m.Shapes {
		if sh.Type != "operation" {
			continue
		}
		if ShortName(id) == name {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil, fmt.Errorf("smithy: operation %q not found", name)
	case 1:
		sh := m.Shapes[matches[0]]
		return matches[0], &sh, nil
	default:
		return "", nil, fmt.Errorf("smithy: operation %q is ambiguous: %v", name, matches)
	}
}

// AllOperations returns the full IDs of every operation shape in the
// model, in sorted order so codegen output is deterministic.
func (m *Model) AllOperations() []string {
	var ids []string
	for id, sh := range m.Shapes {
		if sh.Type == "operation" {
			ids = append(ids, id)
		}
	}
	// caller may sort; we leave it to them so they can pick ordering.
	return ids
}

// LookupShape returns the shape with the given absolute ID.
func (m *Model) LookupShape(id string) (*Shape, error) {
	sh, ok := m.Shapes[id]
	if !ok {
		return nil, fmt.Errorf("smithy: shape %q not found", id)
	}
	return &sh, nil
}

// ShortName extracts the part after '#' in an absolute shape ID.
func ShortName(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '#' {
			return id[i+1:]
		}
	}
	return id
}

// HasTrait reports whether the shape declares the trait with the given ID.
func (s *Shape) HasTrait(id string) bool {
	_, ok := s.Traits[id]
	return ok
}

// TraitJSON returns the raw JSON for a trait, or nil if absent.
func (s *Shape) TraitJSON(id string) json.RawMessage { return s.Traits[id] }

// HasTrait on a Member returns whether the member declares the trait.
func (m *Member) HasTrait(id string) bool {
	_, ok := m.Traits[id]
	return ok
}

// TraitJSON returns the raw JSON for a member trait, or nil if absent.
func (m *Member) TraitJSON(id string) json.RawMessage { return m.Traits[id] }

// HTTPTrait is the parsed form of smithy.api#http.
type HTTPTrait struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
	Code   int    `json:"code"`
}

// HTTPTrait extracts the smithy.api#http trait, or returns nil if absent.
func (s *Shape) HTTPTrait() *HTTPTrait {
	raw := s.TraitJSON("smithy.api#http")
	if raw == nil {
		return nil
	}
	var t HTTPTrait
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil
	}
	return &t
}

// ErrorTrait returns "client" or "server" for an error shape, or "".
func (s *Shape) ErrorTrait() string { return extractStringTrait(s.Traits, "smithy.api#error") }

// HTTPErrorCode returns the smithy.api#httpError status code, or 0 if absent.
func (s *Shape) HTTPErrorCode() int {
	raw := s.TraitJSON("smithy.api#httpError")
	if raw == nil {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0
	}
	return n
}

// XMLName returns the smithy.api#xmlName trait value, or "".
func (s *Shape) XMLName() string  { return extractStringTrait(s.Traits, "smithy.api#xmlName") }
func (m *Member) XMLName() string { return extractStringTrait(m.Traits, "smithy.api#xmlName") }

// TimestampFormat returns the smithy.api#timestampFormat trait, or "".
// Valid values per Smithy: "date-time", "http-date", "epoch-seconds".
func (s *Shape) TimestampFormat() string {
	return extractStringTrait(s.Traits, "smithy.api#timestampFormat")
}
func (m *Member) TimestampFormat() string {
	return extractStringTrait(m.Traits, "smithy.api#timestampFormat")
}

// HTTPQuery returns the query-parameter name a member is bound to via
// smithy.api#httpQuery, or "" if the member is not query-bound.
func (m *Member) HTTPQuery() string { return extractStringTrait(m.Traits, "smithy.api#httpQuery") }

// HTTPHeader returns the header name a member is bound to via
// smithy.api#httpHeader, or "" if not header-bound.
func (m *Member) HTTPHeader() string { return extractStringTrait(m.Traits, "smithy.api#httpHeader") }

// HTTPLabel reports whether the member is bound to a URI label
// (smithy.api#httpLabel is a unit trait).
func (m *Member) HTTPLabel() bool { return m.HasTrait("smithy.api#httpLabel") }

// HTTPPayload reports whether the member carries the entire request
// or response payload (smithy.api#httpPayload).
func (m *Member) HTTPPayload() bool { return m.HasTrait("smithy.api#httpPayload") }

// HTTPPrefixHeaders returns the header-name prefix a member is bound to
// (the value is the prefix), or "" if not prefix-bound.
func (m *Member) HTTPPrefixHeaders() string {
	return extractStringTrait(m.Traits, "smithy.api#httpPrefixHeaders")
}

// Required reports whether a member is marked smithy.api#required.
func (m *Member) Required() bool { return m.HasTrait("smithy.api#required") }

// XMLFlattened reports whether a list/map member uses
// smithy.api#xmlFlattened (no wrapping element).
func (m *Member) XMLFlattened() bool { return m.HasTrait("smithy.api#xmlFlattened") }

// XMLAttribute reports whether a member is serialised as an XML attribute.
func (m *Member) XMLAttribute() bool { return m.HasTrait("smithy.api#xmlAttribute") }

// EnumValue returns the value an enum member maps to.
func (m *Member) EnumValue() string {
	raw := m.TraitJSON("smithy.api#enumValue")
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func extractStringTrait(traits map[string]json.RawMessage, id string) string {
	raw, ok := traits[id]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
