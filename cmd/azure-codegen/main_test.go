package main

import (
	"encoding/json"
	"testing"
)

// TestFlattenARMAllOf_BasicInlining exercises the BUG-20 fix: a
// definition with both allOf and own properties should have the
// referenced schema's properties merged in, with the allOf array
// removed.
func TestFlattenARMAllOf_BasicInlining(t *testing.T) {
	input := `{
		"definitions": {
			"TrackedResource": {
				"type": "object",
				"properties": {
					"location": {"type": "string"},
					"tags": {"type": "object"}
				},
				"required": ["location"]
			},
			"MyResource": {
				"type": "object",
				"allOf": [{"$ref": "#/definitions/TrackedResource"}],
				"properties": {
					"customField": {"type": "string"}
				}
			}
		}
	}`
	out, err := flattenARMAllOf([]byte(input))
	if err != nil {
		t.Fatalf("flattenARMAllOf: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	defs := doc["definitions"].(map[string]any)
	myr := defs["MyResource"].(map[string]any)

	// allOf should be gone.
	if _, hasAllOf := myr["allOf"]; hasAllOf {
		t.Error("MyResource.allOf still present after flattening")
	}
	// properties should now include all three keys.
	props := myr["properties"].(map[string]any)
	for _, k := range []string{"customField", "location", "tags"} {
		if _, ok := props[k]; !ok {
			t.Errorf("MyResource.properties missing %q after flattening", k)
		}
	}
	// required should be merged from the source.
	req, ok := myr["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "location" {
		t.Errorf("MyResource.required after flatten = %v; want [\"location\"]", req)
	}
}

// TestFlattenARMAllOf_PreservesLocalPropertyOnConflict verifies the
// spec-author-override rule: when Child declares a property with
// the same name as Parent, Child's value wins.
func TestFlattenARMAllOf_PreservesLocalPropertyOnConflict(t *testing.T) {
	input := `{
		"definitions": {
			"Parent": {
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "parent name"}
				}
			},
			"Child": {
				"type": "object",
				"allOf": [{"$ref": "#/definitions/Parent"}],
				"properties": {
					"name": {"type": "string", "description": "child override"}
				}
			}
		}
	}`
	out, err := flattenARMAllOf([]byte(input))
	if err != nil {
		t.Fatalf("flattenARMAllOf: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	child := doc["definitions"].(map[string]any)["Child"].(map[string]any)
	props := child["properties"].(map[string]any)
	name := props["name"].(map[string]any)
	if name["description"] != "child override" {
		t.Errorf("Child.properties.name.description = %q; want %q (local-property override should win)",
			name["description"], "child override")
	}
}

// TestFlattenARMAllOf_LeavesUntouchedWhenNoOwnProperties verifies
// the gate: a definition with allOf-only (no own properties) is
// left as-is. The spec author asked for pure inheritance.
func TestFlattenARMAllOf_LeavesUntouchedWhenNoOwnProperties(t *testing.T) {
	input := `{
		"definitions": {
			"Parent": {"type": "object", "properties": {"x": {"type": "string"}}},
			"PureAlias": {"type": "object", "allOf": [{"$ref": "#/definitions/Parent"}]}
		}
	}`
	out, err := flattenARMAllOf([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	pa := doc["definitions"].(map[string]any)["PureAlias"].(map[string]any)
	if _, hasAllOf := pa["allOf"]; !hasAllOf {
		t.Error("PureAlias.allOf was removed; it should have been left alone (no own properties)")
	}
}

// TestFlattenXMSPaths_MovesEntriesToPaths verifies the basic move:
// every key under `x-ms-paths` ends up under `paths` with its
// original key preserved verbatim (the query-string component
// stays in the path key).
func TestFlattenXMSPaths_MovesEntriesToPaths(t *testing.T) {
	input := `{
		"paths": {},
		"x-ms-paths": {
			"/?comp=list": {"get": {"operationId": "List"}},
			"/?restype=service&comp=properties": {"get": {"operationId": "GetServiceProperties"}}
		}
	}`
	out, err := flattenXMSPaths([]byte(input))
	if err != nil {
		t.Fatalf("flattenXMSPaths: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if _, hasXMSPaths := doc["x-ms-paths"]; hasXMSPaths {
		t.Error("x-ms-paths still present after flatten")
	}
	paths := doc["paths"].(map[string]any)
	for _, k := range []string{"/?comp=list", "/?restype=service&comp=properties"} {
		if _, ok := paths[k]; !ok {
			t.Errorf("paths missing %q after flatten", k)
		}
	}
}

// TestFlattenXMSPaths_PathsWinOnConflict verifies that an existing
// paths entry isn't clobbered by an x-ms-paths entry with the
// same key.
func TestFlattenXMSPaths_PathsWinOnConflict(t *testing.T) {
	input := `{
		"paths": {"/foo": {"get": {"operationId": "originalGet"}}},
		"x-ms-paths": {"/foo": {"get": {"operationId": "xmsGet"}}}
	}`
	out, err := flattenXMSPaths([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	foo := paths["/foo"].(map[string]any)
	get := foo["get"].(map[string]any)
	if get["operationId"] != "originalGet" {
		t.Errorf("paths entry was overwritten by x-ms-paths; operationId = %q, want %q",
			get["operationId"], "originalGet")
	}
}

// TestDedupeParameterDefNameCollisions_StampsXGoName verifies the
// 12.A.20.ii behavior: when `parameters.<N>` and `definitions.<N>`
// share a name, the parameter gets `x-go-name: <N>Parameter`.
func TestDedupeParameterDefNameCollisions_StampsXGoName(t *testing.T) {
	input := `{
		"definitions": {
			"LeaseDuration": {"type": "string", "enum": ["infinite", "fixed"]}
		},
		"parameters": {
			"LeaseDuration": {
				"name": "x-ms-lease-duration",
				"in": "header",
				"type": "integer"
			},
			"NotColliding": {
				"name": "x-ms-other",
				"in": "header",
				"type": "string"
			}
		}
	}`
	out, err := dedupeParameterDefNameCollisions([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	params := doc["parameters"].(map[string]any)
	colliding := params["LeaseDuration"].(map[string]any)
	if colliding["x-go-name"] != "LeaseDurationParameter" {
		t.Errorf("colliding parameter x-go-name = %v; want LeaseDurationParameter", colliding["x-go-name"])
	}
	other := params["NotColliding"].(map[string]any)
	if _, hasXGo := other["x-go-name"]; hasXGo {
		t.Error("non-colliding parameter got x-go-name stamped — should be left alone")
	}
}

// TestDedupeParameterDefNameCollisions_RespectsExistingXGoName: the
// preprocessor shouldn't clobber an x-go-name the spec author
// already supplied.
func TestDedupeParameterDefNameCollisions_RespectsExistingXGoName(t *testing.T) {
	input := `{
		"definitions": {"Foo": {"type": "string"}},
		"parameters": {
			"Foo": {
				"name": "x-ms-foo",
				"in": "header",
				"type": "string",
				"x-go-name": "AuthoredName"
			}
		}
	}`
	out, err := dedupeParameterDefNameCollisions([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(out, &doc)
	params := doc["parameters"].(map[string]any)
	foo := params["Foo"].(map[string]any)
	if foo["x-go-name"] != "AuthoredName" {
		t.Errorf("authored x-go-name overwritten: got %v, want AuthoredName", foo["x-go-name"])
	}
}

// TestPromoteXMsEnumName_PromotesInlineSchema verifies the 12.A.12
// behavior: an inline schema with x-ms-enum.name matching a
// top-level definition is rewritten to a $ref.
func TestPromoteXMsEnumName_PromotesInlineSchema(t *testing.T) {
	input := `{
		"definitions": {
			"MyMode": {
				"type": "string",
				"enum": ["A", "B"],
				"x-ms-enum": {"name": "MyMode"}
			},
			"Container": {
				"type": "object",
				"properties": {
					"mode": {
						"type": "string",
						"enum": ["A", "B"],
						"x-ms-enum": {"name": "MyMode"}
					}
				}
			}
		}
	}`
	out, err := promoteXMsEnumName([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(out, &doc)
	mode := doc["definitions"].(map[string]any)["Container"].(map[string]any)["properties"].(map[string]any)["mode"].(map[string]any)
	if mode["$ref"] != "#/definitions/MyMode" {
		t.Errorf("inline schema not promoted: got %v", mode)
	}
}

// TestPromoteXMsEnumName_SkipsParameters verifies the 12.A.20.i fix:
// promotion is suppressed under `parameters` (a parameter with
// x-ms-enum.name matching a definition must NOT be rewritten to
// $ref-to-schema — v2/v3 reject parameter refs that point at
// schemas).
func TestPromoteXMsEnumName_SkipsParameters(t *testing.T) {
	input := `{
		"definitions": {
			"AccessTier": {
				"type": "string",
				"enum": ["Hot", "Cool"],
				"x-ms-enum": {"name": "AccessTier"}
			}
		},
		"parameters": {
			"AccessTierOptional": {
				"name": "x-ms-access-tier",
				"in": "header",
				"type": "string",
				"enum": ["Hot", "Cool"],
				"x-ms-enum": {"name": "AccessTier"}
			}
		}
	}`
	out, err := promoteXMsEnumName([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(out, &doc)
	param := doc["parameters"].(map[string]any)["AccessTierOptional"].(map[string]any)
	if _, hasRef := param["$ref"]; hasRef {
		t.Error("parameter incorrectly rewritten to $ref-to-schema; promotion must be suppressed under `parameters`")
	}
	// The inline enum + x-ms-enum should still be present on the parameter.
	if param["type"] != "string" {
		t.Error("parameter lost its type field")
	}
}

// TestFlattenXMSPaths_NoOp verifies a spec without x-ms-paths
// returns the input unchanged.
func TestFlattenXMSPaths_NoOp(t *testing.T) {
	input := `{"paths": {"/foo": {"get": {"operationId": "Foo"}}}}`
	out, err := flattenXMSPaths([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	// Re-parse and re-marshal both to normalize JSON formatting.
	var in, after map[string]any
	_ = json.Unmarshal([]byte(input), &in)
	_ = json.Unmarshal(out, &after)
	inN, _ := json.Marshal(in)
	afterN, _ := json.Marshal(after)
	if string(inN) != string(afterN) {
		t.Errorf("spec without x-ms-paths was modified:\n  in:  %s\n  out: %s", inN, afterN)
	}
}

// TestClassifyRef covers the parser that decides how each $ref in
// an Azure spec should be resolved. Every shape Azure ships maps
// to one of the three refKinds.
func TestClassifyRef(t *testing.T) {
	cases := []struct {
		name        string
		ref         string
		fromCTVer   string // non-empty when scanning inside a common-types file
		currentDir  string
		wantTarget  string
		wantVersion string
		wantKind    refKind
	}{
		{
			name:     "local-pointer",
			ref:      "#/definitions/Foo",
			wantKind: refLocal,
		},
		{
			name:     "examples-skipped",
			ref:      "./examples/Foo.json",
			wantKind: refLocal,
		},
		{
			name:     "examples-no-dot-prefix-also-skipped",
			ref:      "examples/Foo.json",
			wantKind: refLocal,
		},
		{
			name:        "common-types-fullpath",
			ref:         "../../../../../../common-types/resource-management/v4/types.json#/definitions/TrackedResource",
			wantTarget:  "types.json",
			wantVersion: "v4",
			wantKind:    refCommonTypes,
		},
		{
			name:        "common-types-cross-version-from-CT",
			ref:         "../v5/types.json#/definitions/Resource",
			fromCTVer:   "v6",
			wantTarget:  "types.json",
			wantVersion: "v5",
			wantKind:    refCommonTypes,
		},
		{
			name:        "common-types-same-version-from-CT",
			ref:         "./types.json#/definitions/Resource",
			fromCTVer:   "v4",
			wantTarget:  "types.json",
			wantVersion: "v4",
			wantKind:    refCommonTypes,
		},
		{
			name:       "sibling-from-main-spec",
			ref:        "./CommonDefinitions.json#/definitions/ExtendedLocation",
			wantTarget: "CommonDefinitions.json",
			wantKind:   refSibling,
		},
		{
			name:     "unrecognized-falls-through-to-local",
			ref:      "https://example.com/spec.json",
			wantKind: refLocal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tgt, ver, kind := classifyRef(tc.ref, tc.fromCTVer, tc.currentDir)
			if kind != tc.wantKind {
				t.Errorf("kind = %v, want %v", kind, tc.wantKind)
			}
			if tgt != tc.wantTarget {
				t.Errorf("target = %q, want %q", tgt, tc.wantTarget)
			}
			if ver != tc.wantVersion {
				t.Errorf("version = %q, want %q", ver, tc.wantVersion)
			}
		})
	}
}

// TestFlattenARMAllOf_ChainedInheritance verifies the iterate-until-
// fixpoint behavior: X → allOf [Y]; Y → allOf [Z] resolves the
// full chain in one preprocessor pass.
func TestFlattenARMAllOf_ChainedInheritance(t *testing.T) {
	input := `{
		"definitions": {
			"Z": {"type": "object", "properties": {"z_field": {"type": "string"}}},
			"Y": {
				"type": "object",
				"allOf": [{"$ref": "#/definitions/Z"}],
				"properties": {"y_field": {"type": "string"}}
			},
			"X": {
				"type": "object",
				"allOf": [{"$ref": "#/definitions/Y"}],
				"properties": {"x_field": {"type": "string"}}
			}
		}
	}`
	out, err := flattenARMAllOf([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	x := doc["definitions"].(map[string]any)["X"].(map[string]any)
	props := x["properties"].(map[string]any)
	for _, k := range []string{"x_field", "y_field", "z_field"} {
		if _, ok := props[k]; !ok {
			t.Errorf("X.properties missing %q after chained inheritance flattening", k)
		}
	}
	if _, hasAllOf := x["allOf"]; hasAllOf {
		t.Error("X.allOf still present after chained flatten")
	}
}
