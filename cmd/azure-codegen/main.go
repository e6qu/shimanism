// Command azure-codegen reads an Azure OpenAPI v2 (Swagger) JSON spec,
// converts it to OpenAPI v3 in memory, and runs `oapi-codegen` as a
// library to emit Go types + std-net-http server stubs.
//
// Azure publishes data-plane specs as Swagger 2.0 today
// (https://github.com/Azure/azure-rest-api-specs). oapi-codegen
// consumes only OpenAPI v3, so the conversion step is required
// before generation. `kin-openapi/openapi2conv.ToV3` does that
// conversion canonically — same path the OpenAPI tooling ecosystem
// uses.
//
// Usage:
//
//	azure-codegen \
//	  -spec=services/secrets/spec/azure-keyvault-secrets.json \
//	  -out=services/secrets/gen/azure_keyvault.gen.go \
//	  -pkg=gen
//
// Phase 11.4 pilot. Single-service driver for now (Key Vault
// secrets surface). When the pattern proves out across additional
// Azure services, this command's flags can grow a manifest input
// the way cmd/codegen does.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oapi-codegen/oapi-codegen/v2/pkg/codegen"
)

func main() {
	var (
		specPath        = flag.String("spec", "", "path to the OpenAPI v2 (Swagger) JSON spec")
		outPath         = flag.String("out", "", "path to the generated .gen.go file")
		pkgName         = flag.String("pkg", "gen", "Go package name for the generated file")
		commit          = flag.String("commit", "", "optional upstream commit SHA to record in the file header")
		commonTypesRoot = flag.String("common-types", "services/common-types", "directory containing vendored Azure common-types/resource-management/v<N>/*.json")
	)
	flag.Parse()
	if *specPath == "" || *outPath == "" {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "\n-spec and -out are required")
		os.Exit(2)
	}

	raw, err := os.ReadFile(*specPath)
	if err != nil {
		fail("read %s: %v", *specPath, err)
	}

	// Azure ARM specs $ref Azure's shared common-types
	// (common-types/resource-management/v<N>/*.json) by a relative
	// path that mirrors the upstream repo layout, and split larger
	// services across sibling spec files in the same directory
	// (e.g. ContainerApps.json $refs ./CommonDefinitions.json). We
	// inline both kinds of external references at the v2 layer
	// before converting to v3, so kin-openapi's ToV3 sees a
	// self-contained Swagger doc and emits self-contained v3
	// parameters / definitions. Doing this at the v2 layer (rather
	// than via openapi3.Loader's external-ref resolution after
	// ToV3) keeps the v2 form of each reusable parameter intact
	// (`type: string` rather than the v3 shape
	// `schema: {type: string}`), which the v2→v3 converter then
	// handles correctly.
	if strings.Contains(string(raw), "common-types/") || strings.Contains(string(raw), `"$ref": "./`) {
		specDir, _ := filepath.Abs(filepath.Dir(*specPath))
		inlined, err := inlineExternalRefs(raw, *commonTypesRoot, specDir)
		if err != nil {
			fail("inline external refs: %v", err)
		}
		raw = inlined
	}

	// Promote `x-ms-enum.name` → `x-go-name` so oapi-codegen honours
	// the spec author's intended Go type name. Without this, inline
	// enums get a name derived from the property path (e.g.
	// `HighAvailabilityMode` for `HighAvailability.properties.mode`)
	// which can collide with a standalone definition of the same
	// name. The Azure convention is that `x-ms-enum.name` is
	// authoritative; oapi-codegen honours `x-go-name` natively.
	if strings.Contains(string(raw), "x-ms-enum") {
		promoted, err := promoteXMsEnumName(raw)
		if err != nil {
			fail("promote x-ms-enum.name: %v", err)
		}
		raw = promoted
	}

	// Resolve typename collisions between top-level `definitions.<N>`
	// and top-level `parameters.<N>` by stamping `x-go-name` on the
	// colliding parameter. Azure's Blob spec ships, e.g., both
	// `definitions.LeaseDuration` (a string enum) and
	// `parameters.LeaseDuration` (an integer header parameter).
	// oapi-codegen would emit two `type LeaseDuration` declarations
	// and fail with `duplicate typename`. The schema is the
	// authoritative reuse target; rename the parameter.
	if strings.Contains(string(raw), `"parameters"`) {
		dedup, err := dedupeParameterDefNameCollisions(raw)
		if err != nil {
			fail("dedupe parameter/def names: %v", err)
		}
		raw = dedup
	}

	// Inline ARM `allOf: [{$ref: TrackedResource}]` patterns so
	// oapi-codegen emits a struct instead of a `type X = Y` alias.
	// Azure ARM resource definitions consistently use:
	//   { type: object, allOf: [{$ref: ...}], properties: {own props...} }
	// oapi-codegen sees the 1-element allOf and aliases the schema
	// to the referenced type, discarding the schema's own properties.
	// Merging the referenced schema's properties into the local
	// definition at v2 time produces a flat schema that oapi-codegen
	// emits as a proper Go struct. Required for ARM adapter migration
	// (BUG-20).
	if strings.Contains(string(raw), `"allOf"`) {
		flattened, err := flattenARMAllOf(raw)
		if err != nil {
			fail("flatten ARM allOf: %v", err)
		}
		raw = flattened
	}

	// Flatten `x-ms-paths` into `paths`. Azure's data-plane specs
	// (Blob Storage, Queue Storage, Table Storage, …) use
	// `x-ms-paths` to disambiguate same-URL operations by query
	// parameter — e.g. `/?restype=service&comp=properties` vs
	// `/?comp=list`. Plain OpenAPI v2 / v3 doesn't model that; the
	// generated server has zero operations if we leave the entries
	// under x-ms-paths. Move them to `paths` as-is so the path keys
	// stay distinct (the query-string component is part of the
	// path-key string from OpenAPI's perspective). Downstream
	// dispatch must still match requests against the
	// query-parameter-discriminated route — that's a runtime
	// concern out of scope here; the generator now sees every
	// operation declared by the spec.
	if strings.Contains(string(raw), `"x-ms-paths"`) {
		flat, err := flattenXMSPaths(raw)
		if err != nil {
			fail("flatten x-ms-paths: %v", err)
		}
		raw = flat
	}

	// Parse the v2 spec, convert to v3 in-memory. openapi2conv.ToV3 is
	// the canonical path Azure SDK teams + the OpenAPI ecosystem use
	// to bridge Swagger to v3 generators.
	var v2 openapi2.T
	if err := v2.UnmarshalJSON(raw); err != nil {
		fail("parse v2 spec: %v", err)
	}
	v3, err := openapi2conv.ToV3(&v2)
	if err != nil {
		fail("convert v2 → v3: %v", err)
	}
	// kin-openapi's v2→v3 converter occasionally attaches an empty
	// `AllOf: []` to scalar (enum) schemas. oapi-codegen's MergeSchemas
	// then panics when indexing `allOf[0]` on an empty slice. Walk the
	// converted spec and nil any empty AllOf so generation can proceed.
	normalizeAllOf(v3)

	cfg := codegen.Configuration{
		PackageName: *pkgName,
		Generate: codegen.GenerateOptions{
			Models:        true,
			StdHTTPServer: true,
		},
		OutputOptions: codegen.OutputOptions{
			SkipFmt: false,
		},
	}

	src, err := codegen.Generate(v3, cfg)
	if err != nil {
		fail("oapi-codegen generate: %v", err)
	}

	header := fmt.Sprintf("// Code generated by cmd/azure-codegen from %s.\n", filepath.Base(*specPath))
	if *commit != "" {
		header += fmt.Sprintf("// Upstream commit: %s.\n", *commit)
	}
	header += "// Source spec converted from OpenAPI v2 (Swagger) to v3 via kin-openapi/openapi2conv.ToV3.\n// DO NOT EDIT.\n\n"
	// codegen.Generate emits its own package declaration; replace the
	// header line so our provenance comment sits above the package.
	if idx := strings.Index(src, "package "); idx >= 0 {
		src = header + src[idx:]
	} else {
		src = header + src
	}

	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fail("mkdir %s: %v", filepath.Dir(*outPath), err)
	}
	if err := os.WriteFile(*outPath, []byte(src), 0o644); err != nil {
		fail("write %s: %v", *outPath, err)
	}

	fmt.Printf("wrote %s (%d bytes)\n", *outPath, len(src))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "azure-codegen: "+format+"\n", args...)
	os.Exit(1)
}

// flattenARMAllOf walks every top-level `definitions.<N>` and, when
// a definition declares both `allOf` (with one or more $refs into
// the same `definitions.` namespace) AND its own `properties` (or
// other schema fields beyond description/type), inlines the
// referenced schemas' properties + required + additionalProperties
// into the local definition, then removes the allOf array.
//
// Why this stage exists: oapi-codegen sees the canonical Azure ARM
// resource pattern
//
//	{ type: object, allOf: [{$ref: TrackedResource}], properties: {own} }
//
// and emits `type X = TrackedResource` — a Go type alias that
// silently discards the schema's `properties`. The alias compiles
// and the gen file passes the ServerInterface method-count smoke
// test, but request bodies decoded into `X` lose every field that
// was declared in the local `properties` block — making adapter
// migration impossible.
//
// Behaviour:
//   - Cross-file $refs (../../../common-types/...) are skipped
//     here — the upstream inliner (`inlineExternalRefs`) has
//     already rewritten them to `#/definitions/<N>` by this point.
//   - Local properties take precedence on key collision (the spec
//     author's intent — "this property overrides the inherited one").
//   - Inlining is shallow per pass; if a referenced definition is
//     itself allOf-shaped, it gets its own flattening pass through
//     the same iteration.
//   - To converge, we iterate until no definition changes; this
//     handles chains like `X → allOf [Y]; Y → allOf [Z]`.
func flattenARMAllOf(raw []byte) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse spec JSON: %w", err)
	}
	defs, _ := doc["definitions"].(map[string]interface{})
	if len(defs) == 0 {
		return raw, nil
	}

	mergeFrom := func(target, source map[string]interface{}) {
		// Merge source's properties into target's, leaving target's
		// existing keys intact (spec-author override wins).
		if sp, ok := source["properties"].(map[string]interface{}); ok && len(sp) > 0 {
			tp, _ := target["properties"].(map[string]interface{})
			if tp == nil {
				tp = map[string]interface{}{}
			}
			for k, v := range sp {
				if _, exists := tp[k]; !exists {
					tp[k] = v
				}
			}
			target["properties"] = tp
		}
		// Union required field lists.
		if sr, ok := source["required"].([]interface{}); ok && len(sr) > 0 {
			tr, _ := target["required"].([]interface{})
			seen := map[string]bool{}
			for _, x := range tr {
				if s, ok := x.(string); ok {
					seen[s] = true
				}
			}
			for _, x := range sr {
				if s, ok := x.(string); ok && !seen[s] {
					tr = append(tr, s)
					seen[s] = true
				}
			}
			target["required"] = tr
		}
		// Propagate additionalProperties only when target doesn't
		// declare its own.
		if _, has := target["additionalProperties"]; !has {
			if ap, ok := source["additionalProperties"]; ok {
				target["additionalProperties"] = ap
			}
		}
	}

	// Iterate until no definition changes. Each pass merges allOf
	// referents whose source has already had ITS OWN allOf resolved
	// (or never had one). This naturally handles chains like
	// X → allOf [Y]; Y → allOf [Z]: the first pass resolves Y (Z
	// has no allOf), the next pass resolves X (Y's allOf is now
	// gone). Bounded at 32 passes to avoid cycles.
	for pass := 0; pass < 32; pass++ {
		changed := false
		for _, name := range sortedKeys(defs) {
			schema, ok := defs[name].(map[string]interface{})
			if !ok {
				continue
			}
			allOf, ok := schema["allOf"].([]interface{})
			if !ok || len(allOf) == 0 {
				continue
			}
			// Only flatten when the schema has its own substance
			// beyond the allOf — properties, required, etc.
			// Otherwise leave the allOf alone (downstream may want
			// the inheritance preserved).
			hasOwnProps := false
			if p, ok := schema["properties"].(map[string]interface{}); ok && len(p) > 0 {
				hasOwnProps = true
			}
			if !hasOwnProps {
				continue
			}
			// Resolve each allOf element. Skip non-local refs
			// (already inlined by inlineExternalRefs) and defer
			// merging from sources that still have allOf of their
			// own — they'll be processed in a later pass.
			var remaining []interface{}
			merged := false
			for _, item := range allOf {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					remaining = append(remaining, item)
					continue
				}
				ref, ok := itemMap["$ref"].(string)
				if !ok {
					remaining = append(remaining, item)
					continue
				}
				const prefix = "#/definitions/"
				if !strings.HasPrefix(ref, prefix) {
					// External ref the inliner didn't catch; leave as-is.
					remaining = append(remaining, item)
					continue
				}
				target := strings.TrimPrefix(ref, prefix)
				source, ok := defs[target].(map[string]interface{})
				if !ok {
					remaining = append(remaining, item)
					continue
				}
				// If the source still has unresolved allOf, defer
				// merging until the next pass so we don't pull in
				// a partially-resolved view.
				if srcAllOf, ok := source["allOf"].([]interface{}); ok && len(srcAllOf) > 0 {
					remaining = append(remaining, item)
					continue
				}
				mergeFrom(schema, source)
				merged = true
			}
			if merged {
				if len(remaining) == 0 {
					delete(schema, "allOf")
				} else {
					schema["allOf"] = remaining
				}
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return json.Marshal(doc)
}

// dedupeParameterDefNameCollisions scans top-level `parameters.<N>`
// against top-level `definitions.<N>`; when both exist under the same
// name, stamps `x-go-name: "<N>Parameter"` on the parameter. The
// schema is left intact: in the converted v3 doc it becomes
// `components.schemas.<N>` and is the natural reuse target across
// the spec, so renaming the schema would force every $ref to rewrite
// too. Renaming the parameter localises the change to one node.
//
// Azure Blob ships `definitions.LeaseDuration` (string enum,
// "infinite"|"fixed") AND `parameters.LeaseDuration` (integer
// header). oapi-codegen would emit two `type LeaseDuration`
// declarations and fail.
func dedupeParameterDefNameCollisions(raw []byte) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse spec JSON: %w", err)
	}
	defs, _ := doc["definitions"].(map[string]interface{})
	params, _ := doc["parameters"].(map[string]interface{})
	if len(defs) == 0 || len(params) == 0 {
		return raw, nil
	}
	for _, name := range sortedKeys(params) {
		if _, collides := defs[name]; !collides {
			continue
		}
		param, ok := params[name].(map[string]interface{})
		if !ok {
			continue
		}
		if _, alreadySet := param["x-go-name"]; alreadySet {
			continue
		}
		param["x-go-name"] = name + "Parameter"
	}
	return json.Marshal(doc)
}

// flattenXMSPaths moves every entry from the spec's `x-ms-paths`
// object into `paths`. The key is preserved verbatim so the
// query-string component (`?restype=service&comp=properties`) stays
// part of the path key — OpenAPI v2 treats path keys as opaque
// strings for routing purposes, so distinct query strings produce
// distinct path entries. On conflict (a key already in `paths`),
// `paths` wins.
func flattenXMSPaths(raw []byte) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse spec JSON: %w", err)
	}
	xms, ok := doc["x-ms-paths"].(map[string]interface{})
	if !ok {
		return raw, nil
	}
	paths, _ := doc["paths"].(map[string]interface{})
	if paths == nil {
		paths = map[string]interface{}{}
		doc["paths"] = paths
	}
	for k, v := range xms {
		if _, exists := paths[k]; exists {
			continue
		}
		paths[k] = v
	}
	delete(doc, "x-ms-paths")
	return json.Marshal(doc)
}

// sortedKeys returns the map's keys in sorted order. Used during
// the spec walk so iteration order — and therefore which file's
// definitions land in the merged doc first when names collide —
// is deterministic across runs.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(s []string) {
	// Standard library sort.Strings; pulled into a helper so the
	// caller doesn't have to import sort just for this one use.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// promoteXMsEnumName walks the spec and rewrites inline enums whose
// `x-ms-enum.name` matches a top-level `definitions/<N>` declaring
// the same `x-ms-enum.name`. The spec author is declaring "this
// inline schema IS the top-level enum"; replacing the inline with
// `{$ref: #/definitions/<N>}` makes that explicit and prevents
// oapi-codegen from inferring a colliding Go type name from the
// property path.
//
// Stops short of broader x-go-name promotion: putting `x-go-name`
// on a property's referenced enum schema makes oapi-codegen use it
// as the FIELD name when the schema is `$ref`'d from a property,
// which causes its own kind of collision (multiple properties
// referencing the same enum collapse onto a single field name).
// The right level for x-go-name is the call site of each ref, not
// the ref target — out of scope for this preprocessor.
func promoteXMsEnumName(raw []byte) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse spec JSON: %w", err)
	}
	// Index every top-level definition by its x-ms-enum.name.
	// Multiple definitions sharing the same name would themselves
	// collide; the first one wins for the inline-→-ref rewrite.
	defs, _ := doc["definitions"].(map[string]interface{})
	defByXMSName := map[string]string{}
	for name, raw := range defs {
		schema, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		ext, ok := schema["x-ms-enum"].(map[string]interface{})
		if !ok {
			continue
		}
		xmsName, ok := ext["name"].(string)
		if !ok || xmsName == "" {
			continue
		}
		if _, taken := defByXMSName[xmsName]; !taken {
			defByXMSName[xmsName] = name
		}
	}

	// nonSchemaContainerKeys names parent keys whose value is a
	// map/array of parameter or header objects. Such children are
	// NOT schemas, even though they share the schema-like shape
	// (inline `type`/`enum`/`format` + optional `x-ms-enum`). The
	// v2→v3 converter rejects $ref-to-schema substitutions inside
	// parameters or headers — parameter refs must point at
	// parameter objects, header refs at header objects.
	nonSchemaContainerKeys := map[string]bool{
		"parameters": true,
		"headers":    true,
	}

	// resetSchemaKeys names parent keys whose value IS a schema,
	// even when we're descending from a non-schema parameter or
	// header. A body-parameter's `schema` is the canonical case;
	// `items` covers array-typed parameters/headers.
	resetSchemaKeys := map[string]bool{
		"schema": true,
		"items":  true,
	}

	// The walker tracks "non-schema entry depth" — the number of
	// nested non-schema container/entry levels we're inside:
	//   0  → ordinary schema context (promotion allowed)
	//   1  → the parameters/headers container itself (a map of
	//        named non-schemas, or array of non-schema items)
	//   2+ → inside a specific parameter/header object (promotion
	//        on THIS node would mutate the parameter/header)
	//
	// Promotion fires only at depth 0. A `schema` or `items`
	// sub-key resets the depth back to 0 so body-parameter schemas
	// still get the inline-→-ref rewrite.
	var walk func(node interface{}, atTopLevelDefinition bool, nonSchemaDepth int) interface{}
	walk = func(node interface{}, atTopLevelDefinition bool, nonSchemaDepth int) interface{} {
		switch v := node.(type) {
		case map[string]interface{}:
			ext, hasExt := v["x-ms-enum"].(map[string]interface{})
			if hasExt && !atTopLevelDefinition && nonSchemaDepth == 0 {
				if name, ok := ext["name"].(string); ok && name != "" {
					if tgt, sharesName := defByXMSName[name]; sharesName {
						return map[string]interface{}{
							"$ref": "#/definitions/" + tgt,
						}
					}
				}
			}
			for k, sub := range v {
				var childDepth int
				switch {
				case resetSchemaKeys[k]:
					// `schema` / `items` re-enters schema context.
					childDepth = 0
				case nonSchemaContainerKeys[k]:
					// Entering a parameters/headers container.
					// Its children (the map/array entries) are
					// at depth 2 (still inside non-schema).
					childDepth = 1
				case nonSchemaDepth > 0:
					// Stay inside the current non-schema entry.
					childDepth = nonSchemaDepth + 1
					if childDepth > 2 {
						childDepth = 2
					}
				default:
					childDepth = 0
				}
				v[k] = walk(sub, false, childDepth)
			}
			return v
		case []interface{}:
			// Array entries inherit their container's depth. An
			// array under `parameters` (operation-level params)
			// has depth=1 here; each entry is the parameter
			// object, which we mark depth=2.
			entryDepth := nonSchemaDepth
			if nonSchemaDepth == 1 {
				entryDepth = 2
			}
			for i, sub := range v {
				v[i] = walk(sub, false, entryDepth)
			}
			return v
		default:
			return v
		}
	}
	// Walk definitions first with atTopLevelDefinition=true so the
	// standalone enum schemas don't get $ref-rewritten to themselves.
	for k, sub := range defs {
		defs[k] = walk(sub, true, 0)
	}
	// Walk the rest of the doc; inline x-ms-enums in property
	// schemas get the inline→$ref rewrite.
	for k, sub := range doc {
		if k == "definitions" {
			continue
		}
		var childDepth int
		if nonSchemaContainerKeys[k] {
			childDepth = 1
		}
		doc[k] = walk(sub, false, childDepth)
	}
	return json.Marshal(doc)
}

// commonTypesPattern matches a relative `$ref` into the Azure
// common-types tree (e.g.
// `../../../../../../common-types/resource-management/v4/types.json`)
// so the inliner can decide whether to resolve a $ref against the
// vendored directory.
var commonTypesPattern = regexp.MustCompile(`common-types/resource-management/(v[0-9]+)/([A-Za-z0-9_.-]+)`)

// crossVersionPattern matches the `../v<N>/<file>.json` form used
// by common-types files to reference siblings in a different
// version directory.
var crossVersionPattern = regexp.MustCompile(`\.\./(v[0-9]+)/([A-Za-z0-9_.-]+\.json)`)

// sameVersionPattern matches the `./<file>.json` form used by
// common-types files to reference siblings in the same version
// directory.
var sameVersionPattern = regexp.MustCompile(`^\./([A-Za-z0-9_.-]+\.json)`)

// refKind is the dispatch category a $ref falls into after parsing.
type refKind int

const (
	refLocal       refKind = iota // local pointer or unrecognised form
	refCommonTypes                // resolves to a common-types/v<N>/<file>.json
	refSibling                    // resolves to a sibling file in the spec dir
)

// classifyRef parses a $ref string and decides how it should be
// resolved. Returns:
//
//   - target file name (basename, or filename within common-types/v<N>/)
//   - common-types version (only meaningful when kind == refCommonTypes)
//   - kind (refLocal / refCommonTypes / refSibling)
//
// `fromCommonTypesVersion` is non-empty when the $ref appears inside
// a common-types file we're merging (the same-version shorthand
// then resolves against that version). `currentDir` is unused for
// classification (the caller passes it to the file loader); it
// stays in the signature so the inliner can extend the classifier
// later without rewiring call sites.
func classifyRef(ref, fromCommonTypesVersion, currentDir string) (target, ctVersion string, kind refKind) {
	_ = currentDir
	if strings.HasPrefix(ref, "#") {
		return "", "", refLocal
	}
	// `./examples/<file>` and `./<other>/<file>` shapes are
	// x-ms-examples metadata — oapi-codegen ignores them and the
	// inliner has no reason to follow them. Skip anything under a
	// subdirectory.
	if strings.HasPrefix(ref, "./examples/") || strings.HasPrefix(ref, "examples/") {
		return "", "", refLocal
	}
	if m := commonTypesPattern.FindStringSubmatch(ref); m != nil {
		return m[2], m[1], refCommonTypes
	}
	if m := crossVersionPattern.FindStringSubmatch(ref); m != nil {
		return m[2], m[1], refCommonTypes
	}
	path := ref
	if hi := strings.Index(path, "#"); hi >= 0 {
		path = path[:hi]
	}
	if m := sameVersionPattern.FindStringSubmatch(path); m != nil {
		file := m[1]
		// "./<file>.json" inside a common-types file → same version
		// in the common-types tree. Inside the main spec → sibling.
		if fromCommonTypesVersion != "" {
			return file, fromCommonTypesVersion, refCommonTypes
		}
		return file, "", refSibling
	}
	return "", "", refLocal
}

// inlineExternalRefs makes the Swagger doc self-contained by merging
// every external file's `definitions` and `parameters` blocks into
// the main doc, then rewriting external `$ref`s to the equivalent
// local pointer. Two external sources are handled:
//
//  1. Azure common-types under
//     `common-types/resource-management/v<N>/<file>.json` — vendored
//     at `commonTypesRoot`. Identified by the path containing
//     `common-types/`.
//  2. Sibling spec files in the same directory as the main spec
//     (e.g. `./CommonDefinitions.json`). Identified by the bare
//     `./<file>.json` prefix when seen from the main doc.
//
// Local refs inside merged blocks stay valid because the source
// names are preserved verbatim. If two source files share a
// definition name the first wins (a deliberate first-write
// semantics; Azure's common-types are namespaced enough that this
// hasn't bitten yet — when it does, the inliner needs a prefixing
// scheme).
//
// Two passes:
//  1. Walk the doc + every merged file's content, find every
//     external `$ref`, and ensure the target file's
//     `definitions`/`parameters` blocks are merged in.
//  2. Walk the doc again, rewriting each external `$ref` to the
//     local form (`#/definitions/Foo` or `#/parameters/Foo`).
func inlineExternalRefs(raw []byte, commonTypesRoot, specDir string) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse spec JSON: %w", err)
	}

	cache := map[string]map[string]interface{}{}
	loadFile := func(path string) (map[string]interface{}, error) {
		if d, ok := cache[path]; ok {
			return d, nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var d map[string]interface{}
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		cache[path] = d
		return d, nil
	}
	commonTypesPath := func(version, file string) string {
		return filepath.Join(commonTypesRoot, "resource-management", version, file)
	}
	siblingPath := func(file string) string {
		return filepath.Join(specDir, file)
	}
	ensureMap := func(parent map[string]interface{}, key string) map[string]interface{} {
		if existing, ok := parent[key].(map[string]interface{}); ok {
			return existing
		}
		m := map[string]interface{}{}
		parent[key] = m
		return m
	}
	defs := ensureMap(doc, "definitions")
	params := ensureMap(doc, "parameters")

	// Pass 1: gather every reachable external $ref + merge. Each
	// merge call is keyed by absolute path to dedupe; the file's
	// definitions + parameters are copied into the main doc, then
	// the file is itself scanned for transitive external refs.
	visitedFiles := map[string]bool{}
	var mergeFile func(path string, fromCommonTypesVersion string) error
	mergeFile = func(path, fromCommonTypesVersion string) error {
		if visitedFiles[path] {
			return nil
		}
		visitedFiles[path] = true
		content, err := loadFile(path)
		if err != nil {
			return err
		}
		// Rewrite every external ref inside the loaded content to its
		// local form BEFORE copying into the doc. Without this step
		// the outer doc walk would re-enter the merged definitions
		// and try to classify their `./<file>.json` shorthand against
		// the spec directory (the wrong context), failing with a
		// file-not-found error.
		rewriteLocal := func(node interface{}) interface{} {
			var walk func(interface{}) interface{}
			walk = func(n interface{}) interface{} {
				switch v := n.(type) {
				case map[string]interface{}:
					if r, ok := v["$ref"].(string); ok {
						if hi := strings.Index(r, "#"); hi > 0 {
							out := make(map[string]interface{}, len(v))
							for k, sub := range v {
								if k == "$ref" {
									out[k] = "#" + r[hi+1:]
								} else {
									out[k] = walk(sub)
								}
							}
							return out
						}
					}
					out := make(map[string]interface{}, len(v))
					for k, sub := range v {
						out[k] = walk(sub)
					}
					return out
				case []interface{}:
					out := make([]interface{}, len(v))
					for i, sub := range v {
						out[i] = walk(sub)
					}
					return out
				default:
					return v
				}
			}
			return walk(node)
		}
		if d, ok := content["definitions"].(map[string]interface{}); ok {
			for name, def := range d {
				if _, exists := defs[name]; !exists {
					defs[name] = rewriteLocal(def)
				}
			}
		}
		if p, ok := content["parameters"].(map[string]interface{}); ok {
			for name, val := range p {
				if _, exists := params[name]; !exists {
					params[name] = rewriteLocal(val)
				}
			}
		}
		// Transitive refs inside the just-merged file. `./<file>.json`
		// resolves against the current file's directory — that's the
		// common-types version directory when fromCommonTypesVersion is
		// set, the spec directory otherwise.
		fileDir := filepath.Dir(path)
		var collect func(node interface{}) error
		collect = func(node interface{}) error {
			switch v := node.(type) {
			case map[string]interface{}:
				if refRaw, ok := v["$ref"].(string); ok {
					if tgt, fromVer, kind := classifyRef(refRaw, fromCommonTypesVersion, fileDir); kind != refLocal {
						switch kind {
						case refCommonTypes:
							if err := mergeFile(commonTypesPath(fromVer, tgt), fromVer); err != nil {
								return err
							}
						case refSibling:
							if err := mergeFile(filepath.Join(fileDir, tgt), ""); err != nil {
								return err
							}
						}
					}
				}
				for _, k := range sortedKeys(v) {
					if err := collect(v[k]); err != nil {
						return err
					}
				}
			case []interface{}:
				for _, sub := range v {
					if err := collect(sub); err != nil {
						return err
					}
				}
			}
			return nil
		}
		return collect(content)
	}
	var collectFromDoc func(node interface{}) error
	collectFromDoc = func(node interface{}) error {
		switch v := node.(type) {
		case map[string]interface{}:
			if refRaw, ok := v["$ref"].(string); ok {
				if tgt, fromVer, kind := classifyRef(refRaw, "", specDir); kind != refLocal {
					switch kind {
					case refCommonTypes:
						if err := mergeFile(commonTypesPath(fromVer, tgt), fromVer); err != nil {
							return err
						}
					case refSibling:
						if err := mergeFile(siblingPath(tgt), ""); err != nil {
							return err
						}
					}
				}
			}
			for _, k := range sortedKeys(v) {
				if err := collectFromDoc(v[k]); err != nil {
					return err
				}
			}
		case []interface{}:
			for _, sub := range v {
				if err := collectFromDoc(sub); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collectFromDoc(doc); err != nil {
		return nil, err
	}

	// Pass 2: rewrite every external $ref (anything with a path
	// component before `#`) to the local pointer that now resolves
	// inside the doc itself. This catches both the main spec's
	// `../../../common-types/v4/types.json#/...` refs and the
	// transitive `../v4/types.json#/...` refs inside merged
	// common-types files.
	var rewrite func(node interface{}) interface{}
	rewrite = func(node interface{}) interface{} {
		switch v := node.(type) {
		case map[string]interface{}:
			if refRaw, ok := v["$ref"].(string); ok {
				hashIdx := strings.Index(refRaw, "#")
				if hashIdx > 0 {
					out := make(map[string]interface{}, len(v))
					for k, sub := range v {
						if k == "$ref" {
							out[k] = "#" + refRaw[hashIdx+1:]
						} else {
							out[k] = rewrite(sub)
						}
					}
					return out
				}
			}
			out := make(map[string]interface{}, len(v))
			for k, sub := range v {
				out[k] = rewrite(sub)
			}
			return out
		case []interface{}:
			out := make([]interface{}, len(v))
			for i, sub := range v {
				out[i] = rewrite(sub)
			}
			return out
		default:
			return v
		}
	}
	return json.Marshal(rewrite(doc))
}

// keep url referenced for future use in the codegen path even when
// the inliner is the active common-types resolver.
var _ = url.Parse

// normalizeAllOf walks every schema in the spec and replaces empty
// AllOf slices with nil. Empty AllOf is semantically a no-op but
// trips oapi-codegen's allOf-aware merge path with an out-of-range
// panic at `allOf[0]`.
func normalizeAllOf(v3 *openapi3.T) {
	visited := map[*openapi3.Schema]bool{}
	var walk func(*openapi3.SchemaRef)
	walk = func(r *openapi3.SchemaRef) {
		if r == nil || r.Value == nil {
			return
		}
		if visited[r.Value] {
			return
		}
		visited[r.Value] = true
		if r.Value.AllOf != nil && len(r.Value.AllOf) == 0 {
			r.Value.AllOf = nil
		}
		for _, sub := range r.Value.AllOf {
			walk(sub)
		}
		for _, sub := range r.Value.AnyOf {
			walk(sub)
		}
		for _, sub := range r.Value.OneOf {
			walk(sub)
		}
		walk(r.Value.Items)
		for _, p := range r.Value.Properties {
			walk(p)
		}
		walk(r.Value.AdditionalProperties.Schema)
	}
	if v3.Components != nil {
		for _, s := range v3.Components.Schemas {
			walk(s)
		}
		for _, p := range v3.Components.Parameters {
			if p.Value != nil {
				walk(p.Value.Schema)
			}
		}
	}
	for _, pi := range v3.Paths.Map() {
		for _, p := range pi.Parameters {
			if p.Value != nil {
				walk(p.Value.Schema)
			}
		}
		for _, op := range pi.Operations() {
			for _, p := range op.Parameters {
				if p.Value != nil {
					walk(p.Value.Schema)
				}
			}
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				for _, mt := range op.RequestBody.Value.Content {
					walk(mt.Schema)
				}
			}
			for _, resp := range op.Responses.Map() {
				if resp.Value == nil {
					continue
				}
				for _, mt := range resp.Value.Content {
					walk(mt.Schema)
				}
				for _, h := range resp.Value.Headers {
					if h.Value != nil {
						walk(h.Value.Schema)
					}
				}
			}
		}
	}
	if v3.Components != nil {
		for _, resp := range v3.Components.Responses {
			if resp.Value == nil {
				continue
			}
			for _, mt := range resp.Value.Content {
				walk(mt.Schema)
			}
			for _, h := range resp.Value.Headers {
				if h.Value != nil {
					walk(h.Value.Schema)
				}
			}
		}
		for _, h := range v3.Components.Headers {
			if h.Value != nil {
				walk(h.Value.Schema)
			}
		}
		for _, rb := range v3.Components.RequestBodies {
			if rb.Value == nil {
				continue
			}
			for _, mt := range rb.Value.Content {
				walk(mt.Schema)
			}
		}
	}
}
