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
