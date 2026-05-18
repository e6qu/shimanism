// Package emit walks a Smithy model and emits Go source — request /
// response types, Backend interfaces, and HTTP handlers — for one or
// more operations.
//
// Scope: every shape kind and trait the AWS S3 Smithy 2.0 model uses.
// Shape kinds: operation, service, resource, structure, list, set, map,
// union, enum, intEnum, plus the smithy.api primitives. Bindings:
// httpQuery, httpHeader, httpLabel, httpPayload, httpPrefixHeaders. XML
// traits: xmlName, xmlFlattened, xmlAttribute, xmlNamespace. Plus
// required, error, httpError, timestampFormat.
//
// Features the codegen does not handle fail loud at generation time —
// never silently. Validation traits (length, range, pattern), AWS
// endpoint-rules traits, deprecated/sensitive/documentation, and the
// httpChecksum/eventPayload protocol extensions are deliberately
// no-ops for code generation: they don't affect Go type signatures or
// handler-dispatch logic. The backend implementation is free to use
// them at runtime.
package emit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strings"
	"text/template"

	"github.com/e6qu/shimanism/internal/codegen/smithy"
)

// Options controls a single codegen run.
type Options struct {
	PackageName  string
	SourceFile   string
	SourceCommit string
	// Operations is the list of operation short-names to emit handlers
	// for. The transitive shape closure for each operation (input,
	// output, declared errors, plus everything they reference) is also
	// emitted as Go types.
	Operations []string
}

// Emit produces gofmt-clean Go source as a single byte slice.
func Emit(model *smithy.Model, opts Options) ([]byte, error) {
	g := newGen(model, opts)
	for _, op := range opts.Operations {
		if err := g.collectOperation(op); err != nil {
			return nil, fmt.Errorf("collect %s: %w", op, err)
		}
	}
	src, err := g.render()
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	formatted, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w\n--- source dump ---\n%s", err, src)
	}
	return formatted, nil
}

type gen struct {
	model      *smithy.Model
	opts       Options
	shapeOrder []string
	shapeSeen  map[string]bool
	operations []operation
}

type operation struct {
	ShortName string
	FullID    string
	Shape     *smithy.Shape
	InputID   string
	OutputID  string
	ErrorIDs  []string
}

func newGen(model *smithy.Model, opts Options) *gen {
	return &gen{model: model, opts: opts, shapeSeen: map[string]bool{}}
}

// serviceShortName returns the short name of the single service shape
// in the model. Spec-conformant Smithy models for an AWS service
// declare exactly one service. Returns "Service" as a generic fallback
// for unusual spec layouts.
func (g *gen) serviceShortName() string {
	for id, sh := range g.model.Shapes {
		if sh.Type == "service" {
			return smithy.ShortName(id)
		}
	}
	return "Service"
}

func (g *gen) collectOperation(opName string) error {
	opID, opShape, err := g.model.LookupOperation(opName)
	if err != nil {
		return err
	}
	op := operation{ShortName: opName, FullID: opID, Shape: opShape}
	if opShape.Input != nil {
		op.InputID = opShape.Input.Target
		if err := g.collectShape(opShape.Input.Target); err != nil {
			return err
		}
	}
	if opShape.Output != nil {
		op.OutputID = opShape.Output.Target
		if err := g.collectShape(opShape.Output.Target); err != nil {
			return err
		}
	}
	for _, e := range opShape.Errors {
		op.ErrorIDs = append(op.ErrorIDs, e.Target)
		if err := g.collectShape(e.Target); err != nil {
			return err
		}
	}
	g.operations = append(g.operations, op)
	return nil
}

func (g *gen) collectShape(id string) error {
	if g.shapeSeen[id] {
		return nil
	}
	if isPrimitiveID(id) {
		return nil
	}
	// smithy.api#Unit is the empty sentinel; skip it just like other
	// smithy.api primitives.
	if id == "smithy.api#Unit" {
		return nil
	}
	sh, err := g.model.LookupShape(id)
	if err != nil {
		return err
	}
	g.shapeSeen[id] = true
	switch sh.Type {
	case "structure", "union":
		for _, name := range sortedKeys(sh.Members) {
			m := sh.Members[name]
			if err := g.collectShape(m.Target); err != nil {
				return err
			}
		}
		g.shapeOrder = append(g.shapeOrder, id)
	case "list", "set":
		if sh.Member != nil {
			if err := g.collectShape(sh.Member.Target); err != nil {
				return err
			}
		}
		g.shapeOrder = append(g.shapeOrder, id)
	case "map":
		if sh.Key != nil {
			if err := g.collectShape(sh.Key.Target); err != nil {
				return err
			}
		}
		if sh.Value != nil {
			if err := g.collectShape(sh.Value.Target); err != nil {
				return err
			}
		}
		g.shapeOrder = append(g.shapeOrder, id)
	case "enum", "intEnum":
		g.shapeOrder = append(g.shapeOrder, id)
	case "string", "integer", "long", "short", "boolean", "timestamp", "blob",
		"double", "float", "byte", "bigInteger", "bigDecimal", "document":
		// Primitive aliases / wrappers. No Go type emitted; uses resolve
		// to the underlying Go primitive at the use site.
	case "service", "resource":
		// Topology nodes, not data shapes.
	default:
		return fmt.Errorf("unsupported shape kind %q for %s", sh.Type, id)
	}
	return nil
}

// ============================================================================
// View structs — shape-to-template data flow.
// ============================================================================

type fileData struct {
	Pkg     string
	Source  string
	Commit  string
	Enums   []enumView
	Structs []structView
	Unions  []unionView
	Lists   []listView
	Maps    []mapView
	Ops     []opView
	Errors  []errorView
	// Service is the Smithy service short name, used to name the
	// generated RegisterRoutes helper (e.g. RegisterAmazonS3).
	Service string
}

type enumView struct {
	GoName  string
	Members []enumMember
}

type enumMember struct {
	GoName     string
	Value      string
	ParentType string // the enum's Go name, used to type the const
}

type structView struct {
	GoName       string
	XMLName      string
	XMLNamespace string
	Fields       []fieldView
	IsError      bool
	HTTPError    int
}

type fieldView struct {
	GoName  string
	GoType  string
	// Binding is one of:
	//   "body"           appears in the XML body (default)
	//   "header"         single header value
	//   "label"          URI template segment
	//   "query"          URL query parameter
	//   "payload"        carries the whole request/response body
	//   "prefix-headers" map of headers sharing a prefix
	//   "attribute"      XML attribute on the parent element
	Binding  string
	BindKey  string
	XMLTag   string
	TSFormat string
	Required bool
}

type unionView struct {
	GoName  string
	XMLName string
	Members []fieldView
}

type listView struct {
	GoName     string
	ElemGoType string
	ElemXML    string
}

type mapView struct {
	GoName  string
	KeyType string
	ValType string
}

type opView struct {
	GoName     string
	InputType  string
	OutputType string
	Method     string
	URIPath    string
	HTTPStatus int
	Errors     []errorRef
	Bindings   []fieldView // input bindings (one per input member)

	// Input body-handling.
	InputPayload         *fieldView // single member with httpPayload (or nil)
	InputHasBodyFields   bool       // input struct has any XML-body fields
	OutputPayload        *fieldView // single member with httpPayload on the output (or nil)
	OutputHasBodyFields  bool       // output has any XML-body fields
	OutputHeaders        []fieldView // header-bound output fields
	OutputPrefixHeaders  []fieldView // prefix-headers-bound output fields (Metadata)

	// RequiredInputHeaders are HTTP header names the input declares as
	// required + header-bound. Used by Register() to disambiguate
	// operations that share method + path + query.
	RequiredInputHeaders []string
	// RequiredInputQueries are URL query parameter names the input
	// declares as required + query-bound. Same disambiguation role as
	// RequiredInputHeaders, but for query params (e.g. UploadPart's
	// partNumber + uploadId).
	RequiredInputQueries []string
	// ForbiddenQueries are query parameter names that, if present,
	// disqualify this route. Used by base object/bucket operations
	// (GetObject, PutObject, HeadObject, DeleteObject) to reject S3
	// feature-config queries (`?tagging`, `?acl`, …) that name
	// out-of-intersection sibling operations not in the manifest.
	// Without this, a request like `GET /bucket/key?tagging` falls
	// through to GetObject and the shim silently returns the object
	// body — a fidelity break. See BUGS.md BUG-1.
	ForbiddenQueries []string
}

type errorRef struct {
	GoName    string
	HTTPError int
}

type errorView struct {
	GoName    string
	HTTPError int
}

func (g *gen) render() ([]byte, error) {
	data := fileData{
		Pkg:     g.opts.PackageName,
		Source:  g.opts.SourceFile,
		Commit:  g.opts.SourceCommit,
		Service: g.serviceShortName(),
	}

	for _, id := range g.shapeOrder {
		sh := g.model.Shapes[id]
		switch sh.Type {
		case "enum":
			data.Enums = append(data.Enums, g.enumView(id, &sh))
		case "structure":
			sv, err := g.structView(id, &sh)
			if err != nil {
				return nil, err
			}
			data.Structs = append(data.Structs, sv)
			if sv.IsError {
				data.Errors = append(data.Errors, errorView{GoName: sv.GoName, HTTPError: sv.HTTPError})
			}
		case "union":
			uv, err := g.unionView(id, &sh)
			if err != nil {
				return nil, err
			}
			data.Unions = append(data.Unions, uv)
		case "list", "set":
			lv, err := g.listView(id, &sh)
			if err != nil {
				return nil, err
			}
			data.Lists = append(data.Lists, lv)
		case "map":
			mv, err := g.mapView(id, &sh)
			if err != nil {
				return nil, err
			}
			data.Maps = append(data.Maps, mv)
		}
	}

	for _, op := range g.operations {
		ov, err := g.opView(op)
		if err != nil {
			return nil, err
		}
		data.Ops = append(data.Ops, ov)
	}

	tmpl, err := template.New("file").Funcs(funcs).Parse(fileTemplate)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (g *gen) enumView(id string, sh *smithy.Shape) enumView {
	v := enumView{GoName: smithy.ShortName(id)}
	for _, name := range sortedKeys(sh.Members) {
		m := sh.Members[name]
		val := m.EnumValue()
		if val == "" {
			val = name
		}
		v.Members = append(v.Members, enumMember{
			GoName:     v.GoName + exportName(name),
			Value:      val,
			ParentType: v.GoName,
		})
	}
	return v
}

func (g *gen) structView(id string, sh *smithy.Shape) (structView, error) {
	v := structView{GoName: smithy.ShortName(id), XMLName: sh.XMLName()}
	if raw := sh.TraitJSON("smithy.api#xmlNamespace"); raw != nil {
		var ns struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(raw, &ns)
		v.XMLNamespace = ns.URI
	}
	if sh.HasTrait("smithy.api#error") {
		v.IsError = true
		v.HTTPError = sh.HTTPErrorCode()
	}
	for _, name := range sortedKeys(sh.Members) {
		fv, err := g.fieldView(name, sh.Members[name])
		if err != nil {
			return structView{}, fmt.Errorf("%s.%s: %w", smithy.ShortName(id), name, err)
		}
		v.Fields = append(v.Fields, fv)
	}
	return v, nil
}

func (g *gen) unionView(id string, sh *smithy.Shape) (unionView, error) {
	v := unionView{GoName: smithy.ShortName(id), XMLName: sh.XMLName()}
	for _, name := range sortedKeys(sh.Members) {
		fv, err := g.fieldView(name, sh.Members[name])
		if err != nil {
			return unionView{}, fmt.Errorf("%s.%s: %w", smithy.ShortName(id), name, err)
		}
		v.Members = append(v.Members, fv)
	}
	return v, nil
}

func (g *gen) listView(id string, sh *smithy.Shape) (listView, error) {
	v := listView{GoName: smithy.ShortName(id)}
	elemType, err := g.goTypeForRef(sh.Member.Target, false)
	if err != nil {
		return listView{}, err
	}
	v.ElemGoType = strings.TrimPrefix(elemType, "*")
	xml := sh.Member.XMLName()
	if xml == "" {
		xml = smithy.ShortName(sh.Member.Target)
	}
	v.ElemXML = xml
	return v, nil
}

func (g *gen) mapView(id string, sh *smithy.Shape) (mapView, error) {
	v := mapView{GoName: smithy.ShortName(id)}
	kt, err := g.goTypeForRef(sh.Key.Target, true)
	if err != nil {
		return mapView{}, fmt.Errorf("map key: %w", err)
	}
	vt, err := g.goTypeForRef(sh.Value.Target, true)
	if err != nil {
		return mapView{}, fmt.Errorf("map value: %w", err)
	}
	v.KeyType = kt
	v.ValType = vt
	return v, nil
}

func (g *gen) fieldView(name string, m smithy.Member) (fieldView, error) {
	fv := fieldView{
		GoName:   exportName(name),
		Required: m.Required(),
		TSFormat: m.TimestampFormat(),
	}
	required := m.Required()
	// The AWS REST-XML protocol defaults timestampFormat by binding
	// location: "http-date" for header bindings, "date-time" for body
	// bindings. Apply that default here if the member doesn't declare
	// one explicitly. The runtime helpers honor an empty format as
	// "date-time", so only header bindings need an override.
	tsDefault := func(binding string) {
		if fv.TSFormat == "" && binding == "header" {
			fv.TSFormat = "http-date"
		}
	}
	_ = tsDefault

	// Decide binding first; that determines whether the field appears
	// in XML at all.
	switch {
	case m.HTTPLabel():
		fv.Binding = "label"
		fv.BindKey = name
	case m.HTTPQuery() != "":
		fv.Binding = "query"
		fv.BindKey = m.HTTPQuery()
	case m.HTTPHeader() != "":
		fv.Binding = "header"
		fv.BindKey = m.HTTPHeader()
		tsDefault("header")
	case m.HTTPPrefixHeaders() != "":
		fv.Binding = "prefix-headers"
		fv.BindKey = m.HTTPPrefixHeaders()
	case m.HTTPPayload():
		fv.Binding = "payload"
	case m.XMLAttribute():
		fv.Binding = "attribute"
		xml := m.XMLName()
		if xml == "" {
			xml = name
		}
		fv.XMLTag = xml + ",attr"
	default:
		fv.Binding = "body"
		xml := m.XMLName()
		if xml == "" {
			xml = name
		}
		// Flattened list/map: the parent member tag matches the element
		// name, not the container. Element name comes from the target
		// list's member.xmlName trait (or its target's short name).
		if m.XMLFlattened() {
			targetSh, err := g.model.LookupShape(m.Target)
			if err == nil && (targetSh.Type == "list" || targetSh.Type == "set") && targetSh.Member != nil {
				en := targetSh.Member.XMLName()
				if en == "" {
					en = smithy.ShortName(targetSh.Member.Target)
				}
				xml = en
			}
		}
		fv.XMLTag = xml + ",omitempty"
	}

	// Streaming for httpPayload + blob: the handler should not
	// buffer the body. Generate `io.ReadCloser` for both input (set
	// to r.Body) and output (the caller streams + closes).
	if fv.Binding == "payload" && g.isBlobTarget(m.Target) {
		fv.GoType = "io.ReadCloser"
		return fv, nil
	}

	// Compute the Go type. For body fields targeting a flattened list,
	// expose it as []Element directly (no wrapper).
	if fv.Binding == "body" && m.XMLFlattened() {
		targetSh, err := g.model.LookupShape(m.Target)
		if err == nil && (targetSh.Type == "list" || targetSh.Type == "set") && targetSh.Member != nil {
			elemType, err := g.goTypeForRef(targetSh.Member.Target, false)
			if err != nil {
				return fieldView{}, err
			}
			fv.GoType = "[]" + strings.TrimPrefix(elemType, "*")
			return fv, nil
		}
	}

	goType, err := g.goTypeForRef(m.Target, required)
	if err != nil {
		return fieldView{}, err
	}
	fv.GoType = goType
	return fv, nil
}

func (g *gen) opView(op operation) (opView, error) {
	v := opView{GoName: op.ShortName}
	// smithy.api#Unit is the empty-shape sentinel used by operations
	// with no input or output. Treat as absent.
	if op.InputID != "" && op.InputID != "smithy.api#Unit" {
		v.InputType = smithy.ShortName(op.InputID)
		in, err := g.model.LookupShape(op.InputID)
		if err == nil && in.Type == "structure" {
			for _, name := range sortedKeys(in.Members) {
				fv, err := g.fieldView(name, in.Members[name])
				if err != nil {
					return opView{}, err
				}
				v.Bindings = append(v.Bindings, fv)
				if fv.Binding == "payload" {
					fvCopy := fv
					v.InputPayload = &fvCopy
				}
				if fv.Binding == "body" {
					v.InputHasBodyFields = true
				}
				if fv.Binding == "header" && fv.Required {
					v.RequiredInputHeaders = append(v.RequiredInputHeaders, fv.BindKey)
				}
				if fv.Binding == "query" && fv.Required {
					v.RequiredInputQueries = append(v.RequiredInputQueries, fv.BindKey)
				}
			}
		}
	}
	if op.OutputID != "" && op.OutputID != "smithy.api#Unit" {
		v.OutputType = smithy.ShortName(op.OutputID)
		out, err := g.model.LookupShape(op.OutputID)
		if err == nil && out.Type == "structure" {
			for _, name := range sortedKeys(out.Members) {
				fv, err := g.fieldView(name, out.Members[name])
				if err != nil {
					return opView{}, err
				}
				switch fv.Binding {
				case "payload":
					fvCopy := fv
					v.OutputPayload = &fvCopy
				case "body":
					v.OutputHasBodyFields = true
				case "header":
					v.OutputHeaders = append(v.OutputHeaders, fv)
				case "prefix-headers":
					v.OutputPrefixHeaders = append(v.OutputPrefixHeaders, fv)
				}
			}
		}
	}
	for _, eid := range op.ErrorIDs {
		esh, err := g.model.LookupShape(eid)
		if err != nil {
			return opView{}, err
		}
		v.Errors = append(v.Errors, errorRef{GoName: smithy.ShortName(eid), HTTPError: esh.HTTPErrorCode()})
	}
	if h := op.Shape.HTTPTrait(); h != nil {
		v.Method = h.Method
		v.URIPath = h.URI // includes any ?query suffix from the spec
		v.HTTPStatus = h.Code
		if v.HTTPStatus == 0 {
			v.HTTPStatus = 200
		}
	}
	v.ForbiddenQueries = forbiddenQueriesFor(v.GoName)
	return v, nil
}

// s3FeatureQueries lists every S3 query parameter that names a
// per-object or per-bucket *feature* operation in S3's REST API. Any
// of these on a base GET/PUT/HEAD/DELETE object/bucket request
// signals an out-of-intersection sibling operation (GetObjectAcl,
// GetObjectTagging, GetBucketPolicy, etc.). The base operation
// declares all of these as forbidden so the router doesn't fall
// through and silently serve the wrong response shape.
//
// The list intentionally **excludes** parameters that are legitimate
// arguments to the base operations themselves: `versionId`,
// `partNumber`, `uploadId`, `response-*` (response-header
// overrides), `x-id` (the SDK's disambiguator we already strip).
var s3FeatureQueries = []string{
	"acl",
	"accelerate",
	"analytics",
	"attributes",
	"cors",
	"encryption",
	"intelligent-tiering",
	"inventory",
	"legal-hold",
	"lifecycle",
	"logging",
	"metrics",
	"notification",
	"object-lock",
	"ownershipControls",
	"policy",
	"policyStatus",
	"publicAccessBlock",
	"replication",
	"requestPayment",
	"restore",
	"retention",
	"select",
	"tagging",
	"torrent",
	"versioning",
	"website",
}

func forbiddenQueriesFor(opName string) []string {
	switch opName {
	case "GetObject", "PutObject", "HeadObject", "DeleteObject", "CopyObject":
		// Object-level base ops: any feature query names a sibling.
		return append([]string(nil), s3FeatureQueries...)
	case "ListObjectsV2", "HeadBucket", "DeleteBucket":
		// Bucket-level base ops: same set applies.
		return append([]string(nil), s3FeatureQueries...)
	}
	return nil
}

// goTypeForRef returns the Go type expression for a member targeting
// the given shape ID. When `required` is true, scalars and enums use
// value types; structs always remain pointers; lists/maps always values.
func (g *gen) goTypeForRef(id string, required bool) (string, error) {
	if prim, ok := primitiveGoType(id); ok {
		if isReferenceType(prim) || required {
			return prim, nil
		}
		return "*" + prim, nil
	}
	sh, err := g.model.LookupShape(id)
	if err != nil {
		return "", err
	}
	switch sh.Type {
	case "string":
		if required {
			return "string", nil
		}
		return "*string", nil
	case "integer":
		if required {
			return "int32", nil
		}
		return "*int32", nil
	case "long":
		if required {
			return "int64", nil
		}
		return "*int64", nil
	case "short":
		if required {
			return "int16", nil
		}
		return "*int16", nil
	case "boolean":
		if required {
			return "bool", nil
		}
		return "*bool", nil
	case "timestamp":
		if required {
			return "time.Time", nil
		}
		return "*time.Time", nil
	case "blob":
		return "[]byte", nil
	case "double":
		if required {
			return "float64", nil
		}
		return "*float64", nil
	case "float":
		if required {
			return "float32", nil
		}
		return "*float32", nil
	case "byte":
		if required {
			return "int8", nil
		}
		return "*int8", nil
	case "bigInteger", "bigDecimal":
		if required {
			return "string", nil
		}
		return "*string", nil
	case "document":
		return "[]byte", nil
	case "structure", "union":
		return "*" + smithy.ShortName(id), nil
	case "list", "set":
		return smithy.ShortName(id), nil
	case "map":
		return smithy.ShortName(id), nil
	case "enum":
		if required {
			return smithy.ShortName(id), nil
		}
		return "*" + smithy.ShortName(id), nil
	case "intEnum":
		if required {
			return smithy.ShortName(id), nil
		}
		return "*" + smithy.ShortName(id), nil
	default:
		return "", fmt.Errorf("unsupported shape kind %q for type ref %s", sh.Type, id)
	}
}

func isReferenceType(t string) bool { return t == "[]byte" }

// isBlobTarget reports whether the given shape ID resolves to a blob
// (either smithy.api#Blob directly or a user-defined alias of blob).
func (g *gen) isBlobTarget(id string) bool {
	if id == "smithy.api#Blob" {
		return true
	}
	sh, err := g.model.LookupShape(id)
	if err != nil {
		return false
	}
	return sh.Type == "blob"
}

func isPrimitiveID(id string) bool { return strings.HasPrefix(id, "smithy.api#") }

func primitiveGoType(id string) (string, bool) {
	switch id {
	case "smithy.api#String":
		return "string", true
	case "smithy.api#Integer", "smithy.api#PrimitiveInteger":
		return "int32", true
	case "smithy.api#Long", "smithy.api#PrimitiveLong":
		return "int64", true
	case "smithy.api#Short", "smithy.api#PrimitiveShort":
		return "int16", true
	case "smithy.api#Boolean", "smithy.api#PrimitiveBoolean":
		return "bool", true
	case "smithy.api#Timestamp":
		return "time.Time", true
	case "smithy.api#Blob", "smithy.api#Document":
		return "[]byte", true
	case "smithy.api#Double", "smithy.api#PrimitiveDouble":
		return "float64", true
	case "smithy.api#Float", "smithy.api#PrimitiveFloat":
		return "float32", true
	case "smithy.api#Byte", "smithy.api#PrimitiveByte":
		return "int8", true
	case "smithy.api#Unit":
		return "struct{}", true
	}
	return "", false
}

func exportName(name string) string {
	if name == "" {
		return name
	}
	r := []rune(name)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - ('a' - 'A')
	}
	return string(r)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var funcs = template.FuncMap{
	"join": strings.Join,
}
