// Package gcp_firestore is the GCP Firestore Native REST/JSON
// frontend for shimanism's NoSQL service. It speaks the HTTP+JSON
// wire protocol that google.golang.org/api/firestore/v1 (the
// Discovery-generated REST SDK) and `gcloud firestore` drive, and
// translates each request into a call on the neutral
// `domain.NoSQL` interface.
//
// Per N18 (docs/normalizations.md), Firestore has no native table
// concept. The shim materialises "tables" via the reserved
// __shim_tables__ collection: writes there are domain.CreateTable
// / UpdateTableTags; reads = GetTable; deletes = DeleteTable; list
// = ListTables. Writes/reads against any other collection go
// through domain.{PutItem,GetItem,DeleteItem,Scan,Query}.
//
// Wire types come from google.golang.org/api/firestore/v1 directly
// per AGENTS.md decision #11. The emitter at
// services/nosql/gen/gcp ships the routing inventory; the dispatch
// here is path-shape inspection (the same pattern the existing GCP
// frontends use).
package gcp_firestore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	firestore "google.golang.org/api/firestore/v1"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/nosql/domain"
	_ "github.com/e6qu/shimanism/services/nosql/gen/gcp" // spec-drift contract; tests pin dispatch shapes against gen.gcp.Routes.
)

// MetadataCollection mirrors the backend's reserved collection name
// for table schema/tags storage (per N18).
const MetadataCollection = "__shim_tables__"

// Server is a Firestore-shaped HTTP frontend dispatching to a
// domain.NoSQL backend.
type Server struct {
	n domain.NoSQL
}

// New returns a frontend bound to the given backend.
func New(n domain.NoSQL) *Server { return &Server{n: n} }

// Handler wraps Server with the GCP bearer verifier middleware.
// SHIMANISM_TEST_UNAUTHENTICATED_GCP=1 short-circuits verification.
func Handler(n domain.NoSQL) http.Handler {
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://firestore.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	return gcpbearer.Middleware(verifier)(New(n))
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if !strings.HasPrefix(path, "v1/projects/") {
		writeError(w, http.StatusNotFound, "notFound", "no route matches "+r.URL.Path)
		return
	}
	rest := strings.TrimPrefix(path, "v1/projects/")
	// rest = "{project}/databases/{db}/documents..."
	segs := strings.SplitN(rest, "/", 4)
	if len(segs) < 4 || segs[1] != "databases" {
		writeError(w, http.StatusNotFound, "notFound", "no route matches "+r.URL.Path)
		return
	}
	// segs[3] starts with "documents" possibly followed by /{collection}/...
	docPart := segs[3]
	// Handle :runQuery / :commit on documents root.
	if docPart == "documents:runQuery" {
		if r.Method == http.MethodPost {
			srv.runQuery(w, r)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
		return
	}
	if !strings.HasPrefix(docPart, "documents") {
		writeError(w, http.StatusNotFound, "notFound", "no route matches "+r.URL.Path)
		return
	}
	rem := strings.TrimPrefix(docPart, "documents")
	rem = strings.TrimPrefix(rem, "/")
	if rem == "" {
		// /documents — Firestore root listing; not in intersection.
		writeError(w, http.StatusBadRequest, "invalid", "listing root documents is out of intersection")
		return
	}
	// rem = "{collection}" OR "{collection}/{docId}"
	parts := strings.SplitN(rem, "/", 2)
	collection := parts[0]
	// :commit could appear at root or per-collection — we don't
	// support :commit for this PR; reject explicitly rather than
	// fabricating success.
	if strings.HasSuffix(collection, ":commit") || strings.Contains(rem, ":commit") {
		writeError(w, http.StatusBadRequest, "invalid", ":commit is out of intersection for 15.C; use createDocument/patch/delete")
		return
	}
	if len(parts) == 1 {
		// /documents/{collection}
		switch r.Method {
		case http.MethodGet:
			srv.listDocuments(w, r, collection)
		case http.MethodPost:
			srv.createDocument(w, r, collection)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
		}
		return
	}
	docID := parts[1]
	// docId can itself contain "/" for subcollections; out of
	// intersection for 15.C.
	if strings.Contains(docID, "/") {
		writeError(w, http.StatusBadRequest, "invalid", "subcollections are out of intersection for 15.C")
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getDocument(w, r, collection, docID)
	case http.MethodPatch:
		srv.patchDocument(w, r, collection, docID)
	case http.MethodDelete:
		srv.deleteDocument(w, r, collection, docID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
	}
}

// -------- Table operations (via __shim_tables__ convention) --------

func (srv *Server) createTableFromDocBody(w http.ResponseWriter, r *http.Request, name string) {
	var doc firestore.Document
	if err := decodeJSON(r, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	opt := domain.CreateTableOptions{}
	if v, ok := doc.Fields["partitionKey"]; ok {
		opt.PartitionKeyName = v.StringValue
	}
	if v, ok := doc.Fields["sortKey"]; ok {
		opt.SortKeyName = v.StringValue
	}
	if v, ok := doc.Fields["description"]; ok {
		opt.Description = v.StringValue
	}
	if v, ok := doc.Fields["tags"]; ok && v.MapValue != nil {
		opt.Tags = map[string]string{}
		for k, vv := range v.MapValue.Fields {
			opt.Tags[k] = vv.StringValue
		}
	}
	t, err := srv.n.CreateTable(r.Context(), name, opt)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeMetaDoc(w, http.StatusOK, t, name)
}

func (srv *Server) getTable(w http.ResponseWriter, r *http.Request, name string) {
	t, err := srv.n.GetTable(r.Context(), name)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeMetaDoc(w, http.StatusOK, t, name)
}

func (srv *Server) deleteTable(w http.ResponseWriter, r *http.Request, name string) {
	// Firestore's DELETE has no force flag; the shim defaults to
	// force=false (Refuse if items exist) so the wire matches the
	// "you can't delete a non-empty collection" Firestore convention.
	if err := srv.n.DeleteTable(r.Context(), name, false); err != nil {
		writeDomainErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

func (srv *Server) listTables(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opt := domain.ListTablesOptions{
		PageSize:  parseInt(q.Get("pageSize")),
		PageToken: q.Get("pageToken"),
		// no NamePrefix on Firestore-side listing.
	}
	res, err := srv.n.ListTables(r.Context(), opt)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	out := &firestore.ListDocumentsResponse{NextPageToken: res.NextPageToken}
	for _, t := range res.Tables {
		out.Documents = append(out.Documents, tableMetaDoc(t, t.Name))
	}
	writeJSON(w, http.StatusOK, out)
}

// -------- Item operations --------

// createDocument with no documentId is rejected (the shim's domain
// requires the partition-key attribute be supplied; auto-generated
// docIDs don't carry that info honestly).
func (srv *Server) createDocument(w http.ResponseWriter, r *http.Request, collection string) {
	if collection == MetadataCollection {
		// CreateTable path. Firestore's createDocument requires
		// documentId via query string OR the documentId path segment.
		docID := r.URL.Query().Get("documentId")
		if docID == "" {
			writeError(w, http.StatusBadRequest, "invalid", "documentId query parameter is required for __shim_tables__ writes")
			return
		}
		srv.createTableFromDocBody(w, r, docID)
		return
	}
	docID := r.URL.Query().Get("documentId")
	if docID == "" {
		// Firestore auto-generates an ID. The shim derives the docID
		// from the item's partition (+ sort) key — that's the only
		// honest mapping. Reject the auto-ID path.
		writeError(w, http.StatusBadRequest, "invalid", "documentId query parameter is required (shim derives docIDs from partition key)")
		return
	}
	var doc firestore.Document
	if err := decodeJSON(r, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	attrs := attrsFromFS(doc.Fields)
	if err := srv.n.PutItem(r.Context(), collection, domain.Item{Attributes: attrs}); err != nil {
		writeDomainErr(w, err)
		return
	}
	// Echo the doc back with a synthetic name.
	out := &firestore.Document{
		Name:       "projects/_/databases/(default)/documents/" + collection + "/" + docID,
		Fields:     doc.Fields,
		CreateTime: time.Now().UTC().Format(time.RFC3339Nano),
		UpdateTime: time.Now().UTC().Format(time.RFC3339Nano),
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) patchDocument(w http.ResponseWriter, r *http.Request, collection, docID string) {
	if collection == MetadataCollection {
		// UpdateTableTags path — only `tags` is patchable.
		var doc firestore.Document
		if err := decodeJSON(r, &doc); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		tags := map[string]string{}
		if v, ok := doc.Fields["tags"]; ok && v.MapValue != nil {
			for k, vv := range v.MapValue.Fields {
				tags[k] = vv.StringValue
			}
		}
		if err := srv.n.UpdateTableTags(r.Context(), docID, tags); err != nil {
			writeDomainErr(w, err)
			return
		}
		t, err := srv.n.GetTable(r.Context(), docID)
		if err != nil {
			writeDomainErr(w, err)
			return
		}
		writeMetaDoc(w, http.StatusOK, t, docID)
		return
	}
	var doc firestore.Document
	if err := decodeJSON(r, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	attrs := attrsFromFS(doc.Fields)
	if err := srv.n.PutItem(r.Context(), collection, domain.Item{Attributes: attrs}); err != nil {
		writeDomainErr(w, err)
		return
	}
	out := &firestore.Document{
		Name:       "projects/_/databases/(default)/documents/" + collection + "/" + docID,
		Fields:     doc.Fields,
		CreateTime: time.Now().UTC().Format(time.RFC3339Nano),
		UpdateTime: time.Now().UTC().Format(time.RFC3339Nano),
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) getDocument(w http.ResponseWriter, r *http.Request, collection, docID string) {
	if collection == MetadataCollection {
		srv.getTable(w, r, docID)
		return
	}
	// Item GET — need the table's schema to translate docID → Key.
	t, err := srv.n.GetTable(r.Context(), collection)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	key, perr := decodeDocIDToKey(docID, t)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "invalid", perr.Error())
		return
	}
	item, err := srv.n.GetItem(r.Context(), collection, key)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	out := &firestore.Document{
		Name:   "projects/_/databases/(default)/documents/" + collection + "/" + docID,
		Fields: fieldsToFS(item.Attributes),
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteDocument(w http.ResponseWriter, r *http.Request, collection, docID string) {
	if collection == MetadataCollection {
		srv.deleteTable(w, r, docID)
		return
	}
	t, err := srv.n.GetTable(r.Context(), collection)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	key, perr := decodeDocIDToKey(docID, t)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "invalid", perr.Error())
		return
	}
	if err := srv.n.DeleteItem(r.Context(), collection, key); err != nil {
		writeDomainErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

func (srv *Server) listDocuments(w http.ResponseWriter, r *http.Request, collection string) {
	if collection == MetadataCollection {
		srv.listTables(w, r)
		return
	}
	q := r.URL.Query()
	opt := domain.ScanOptions{
		Limit:     parseInt(q.Get("pageSize")),
		PageToken: q.Get("pageToken"),
	}
	res, err := srv.n.Scan(r.Context(), collection, opt)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	out := &firestore.ListDocumentsResponse{NextPageToken: res.NextPageToken}
	for _, item := range res.Items {
		docID := docIDFor(item, collection)
		out.Documents = append(out.Documents, &firestore.Document{
			Name:   "projects/_/databases/(default)/documents/" + collection + "/" + docID,
			Fields: fieldsToFS(item.Attributes),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) runQuery(w http.ResponseWriter, r *http.Request) {
	var req firestore.RunQueryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if req.StructuredQuery == nil || len(req.StructuredQuery.From) == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "StructuredQuery.From is required")
		return
	}
	collection := req.StructuredQuery.From[0].CollectionId
	t, err := srv.n.GetTable(r.Context(), collection)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	pkVal, skPrefix, perr := parseStructuredQueryFilter(req.StructuredQuery, t)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "invalid", perr.Error())
		return
	}
	opt := domain.QueryOptions{SortKeyPrefix: skPrefix}
	if req.StructuredQuery.Limit > 0 {
		opt.Limit = int(req.StructuredQuery.Limit)
	}
	res, err := srv.n.Query(r.Context(), collection, pkVal, opt)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	// Firestore's runQuery response is a streaming array of
	// RunQueryResponse objects. Emit as a flat JSON array.
	out := make([]*firestore.RunQueryResponse, 0, len(res.Items))
	for _, item := range res.Items {
		docID := docIDFor(item, collection)
		out = append(out, &firestore.RunQueryResponse{
			Document: &firestore.Document{
				Name:   "projects/_/databases/(default)/documents/" + collection + "/" + docID,
				Fields: fieldsToFS(item.Attributes),
			},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// -------- helpers --------

func parseStructuredQueryFilter(q *firestore.StructuredQuery, t domain.Table) (domain.Value, string, error) {
	if q.Where == nil {
		return domain.Value{}, "", errors.New("where filter required for shim Query (full-collection scans should use documents.list)")
	}
	filters := flatFilters(q.Where)
	var pk domain.Value
	var skPrefix string
	for _, f := range filters {
		ff := f.FieldFilter
		if ff == nil {
			return domain.Value{}, "", errors.New("only FieldFilters in a CompositeFilter AND are supported")
		}
		if ff.Field == nil {
			return domain.Value{}, "", errors.New("FieldFilter.Field required")
		}
		field := ff.Field.FieldPath
		switch ff.Op {
		case "EQUAL":
			if field == t.PartitionKeyName {
				pk = valueFromFSVal(ff.Value)
			} else {
				return domain.Value{}, "", errors.New("EQUAL filter is only supported on the partition key (got " + field + ")")
			}
		case "GREATER_THAN_OR_EQUAL":
			if field != t.SortKeyName {
				return domain.Value{}, "", errors.New("GREATER_THAN_OR_EQUAL only on sort key")
			}
			if ff.Value != nil {
				skPrefix = ff.Value.StringValue
			}
		case "LESS_THAN":
			if field != t.SortKeyName {
				return domain.Value{}, "", errors.New("LESS_THAN only on sort key")
			}
			// LESS_THAN bound is the upper sentinel; we already use the
			// lower as the prefix. Validate but no-op.
		default:
			return domain.Value{}, "", errors.New("unsupported FieldFilter op: " + ff.Op)
		}
	}
	if pk.Type == domain.ValueUnknown {
		return domain.Value{}, "", errors.New("partition-key EQUAL filter required (the shim's NoSQL Query intersection)")
	}
	return pk, skPrefix, nil
}

// flatFilters unwraps a single-level CompositeFilter AND into its
// constituent FieldFilters. Nested composites are out-of-intersection.
func flatFilters(f *firestore.Filter) []*firestore.Filter {
	if f.CompositeFilter != nil && f.CompositeFilter.Op == "AND" {
		return f.CompositeFilter.Filters
	}
	return []*firestore.Filter{f}
}

// docIDFor derives a Firestore docID from an item by encoding its
// partition (+ sort) key. The encoding matches the backend's
// encodeKey so cross-cloud Apply round-trips. The schema (partition
// + sort key names) comes from the table.
func docIDFor(item domain.Item, collection string) string {
	// We don't have the table at this site; but the item carries the
	// values keyed by attribute name. The Firestore *responses* use
	// the docID for round-trip identity, so we just need something
	// stable that the user can echo back. The shim's GCP backend
	// uses base64-url(<typed-prefix-segments>) of the composite key
	// — but we don't know which attrs are the keys here without the
	// table schema. For the response, return a deterministic
	// hash-style identifier built from sorted-attr JSON; consumers
	// only need it to be stable, not parseable.
	//
	// Note: when the user re-reads with GET on this docID, the
	// frontend's getDocument re-derives the key via decodeDocIDToKey,
	// which uses the table schema. So the docID format must be
	// reversible. We solve this by using the same base64-url(typed)
	// encoding as the backend, computed at the frontend using the
	// schema fetched separately. Until that's threaded through, we
	// punt: emit a deterministic hex hash of the attribute values.
	//
	// TODO(15.C cross-cloud cells): align docID format with backend.
	parts := make([]string, 0, len(item.Attributes))
	for k, v := range item.Attributes {
		parts = append(parts, k+"="+v.Type.String()+":"+attrPayload(v))
	}
	// stable order
	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			if parts[j] < parts[i] {
				parts[i], parts[j] = parts[j], parts[i]
			}
		}
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "|")))
}

func attrPayload(v domain.Value) string {
	switch v.Type {
	case domain.ValueString:
		return v.Str
	case domain.ValueNumber:
		return v.Num
	case domain.ValueBool:
		if v.Bool {
			return "1"
		}
		return "0"
	case domain.ValueBytes:
		return base64.StdEncoding.EncodeToString(v.Bin)
	}
	return ""
}

// decodeDocIDToKey reverses the frontend's docIDFor for item GET/DELETE.
// The docID is base64-url(<sorted attrs as 'name=type:payload' separated by '|'>),
// so we can extract the partition key (and sort key, when the schema
// requires) by name.
func decodeDocIDToKey(docID string, t domain.Table) (domain.Key, error) {
	raw, err := base64.RawURLEncoding.DecodeString(docID)
	if err != nil {
		return domain.Key{}, errors.New("docID is not valid base64-url: " + err.Error())
	}
	parts := strings.Split(string(raw), "|")
	attrs := map[string]domain.Value{}
	for _, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		name, encoded := p[:eq], p[eq+1:]
		colon := strings.IndexByte(encoded, ':')
		if colon < 0 {
			continue
		}
		typ, payload := encoded[:colon], encoded[colon+1:]
		switch typ {
		case "string":
			attrs[name] = domain.StringValue(payload)
		case "number":
			attrs[name] = domain.NumberValue(payload)
		case "bool":
			attrs[name] = domain.BoolValue(payload == "1")
		case "bytes":
			b, _ := base64.StdEncoding.DecodeString(payload)
			attrs[name] = domain.BytesValue(b)
		case "null":
			attrs[name] = domain.NullValue()
		}
	}
	pk, ok := attrs[t.PartitionKeyName]
	if !ok {
		return domain.Key{}, errors.New("docID does not encode partition key " + t.PartitionKeyName)
	}
	k := domain.Key{PartitionKey: pk}
	if t.SortKeyName != "" {
		sk, ok := attrs[t.SortKeyName]
		if !ok {
			return domain.Key{}, errors.New("docID does not encode sort key " + t.SortKeyName)
		}
		k.SortKey = sk
	}
	return k, nil
}

// -------- value <-> firestore.Value translation --------

func attrsFromFS(in map[string]firestore.Value) map[string]domain.Value {
	out := make(map[string]domain.Value, len(in))
	for k, v := range in {
		vv := v
		out[k] = valueFromFSVal(&vv)
	}
	return out
}

func valueFromFSVal(v *firestore.Value) domain.Value {
	if v == nil {
		return domain.Value{}
	}
	// Order matters: detect the populated discriminator member. Use
	// JSON-string semantics — Firestore omits zero-valued fields, so a
	// boolean false won't appear unless ForceSendFields says so.
	if v.StringValue != "" {
		return domain.StringValue(v.StringValue)
	}
	if v.IntegerValue != 0 {
		return domain.NumberValue(strconv.FormatInt(v.IntegerValue, 10))
	}
	if v.DoubleValue != 0 {
		return domain.NumberValue(strconv.FormatFloat(v.DoubleValue, 'g', -1, 64))
	}
	if v.BooleanValue {
		return domain.BoolValue(true)
	}
	if v.BytesValue != "" {
		b, _ := base64.StdEncoding.DecodeString(v.BytesValue)
		return domain.BytesValue(b)
	}
	if v.NullValue == "NULL_VALUE" {
		return domain.NullValue()
	}
	for _, f := range v.ForceSendFields {
		switch f {
		case "BooleanValue":
			return domain.BoolValue(v.BooleanValue)
		case "IntegerValue":
			return domain.NumberValue(strconv.FormatInt(v.IntegerValue, 10))
		case "DoubleValue":
			return domain.NumberValue(strconv.FormatFloat(v.DoubleValue, 'g', -1, 64))
		case "StringValue":
			return domain.StringValue(v.StringValue)
		case "NullValue":
			return domain.NullValue()
		}
	}
	return domain.NullValue()
}

func fieldsToFS(in map[string]domain.Value) map[string]firestore.Value {
	out := make(map[string]firestore.Value, len(in))
	for k, v := range in {
		fsv := valueToFSVal(v)
		out[k] = *fsv
	}
	return out
}

func valueToFSVal(v domain.Value) *firestore.Value {
	out := &firestore.Value{}
	switch v.Type {
	case domain.ValueString:
		out.StringValue = v.Str
		out.ForceSendFields = []string{"StringValue"}
	case domain.ValueNumber:
		if n, err := strconv.ParseInt(v.Num, 10, 64); err == nil {
			out.IntegerValue = n
			out.ForceSendFields = []string{"IntegerValue"}
		} else if f, err := strconv.ParseFloat(v.Num, 64); err == nil && strings.ContainsAny(v.Num, ".eE") {
			out.DoubleValue = f
			out.ForceSendFields = []string{"DoubleValue"}
		} else {
			out.StringValue = v.Num
			out.ForceSendFields = []string{"StringValue"}
		}
	case domain.ValueBool:
		out.BooleanValue = v.Bool
		out.ForceSendFields = []string{"BooleanValue"}
	case domain.ValueBytes:
		out.BytesValue = base64.StdEncoding.EncodeToString(v.Bin)
		out.ForceSendFields = []string{"BytesValue"}
	case domain.ValueNull:
		out.NullValue = "NULL_VALUE"
		out.ForceSendFields = []string{"NullValue"}
	}
	return out
}

// -------- table metadata <-> Document --------

func writeMetaDoc(w http.ResponseWriter, status int, t domain.Table, name string) {
	writeJSON(w, status, tableMetaDoc(t, name))
}

func tableMetaDoc(t domain.Table, name string) *firestore.Document {
	doc := &firestore.Document{
		Name:       "projects/_/databases/(default)/documents/" + MetadataCollection + "/" + name,
		Fields:     map[string]firestore.Value{},
		CreateTime: t.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdateTime: t.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	doc.Fields["partitionKey"] = firestore.Value{StringValue: t.PartitionKeyName, ForceSendFields: []string{"StringValue"}}
	doc.Fields["sortKey"] = firestore.Value{StringValue: t.SortKeyName, ForceSendFields: []string{"StringValue"}}
	doc.Fields["description"] = firestore.Value{StringValue: t.Description, ForceSendFields: []string{"StringValue"}}
	if len(t.Tags) > 0 {
		tagFields := map[string]firestore.Value{}
		for k, v := range t.Tags {
			tagFields[k] = firestore.Value{StringValue: v, ForceSendFields: []string{"StringValue"}}
		}
		doc.Fields["tags"] = firestore.Value{MapValue: &firestore.MapValue{Fields: tagFields}, ForceSendFields: []string{"MapValue"}}
	}
	return doc
}

// -------- error envelope --------

func writeError(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	envelope := map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": message,
			"status":  reasonStatus(status),
			"errors": []map[string]any{
				{
					"message": message,
					"reason":  reason,
					"domain":  "global",
				},
			},
		},
	}
	_ = json.NewEncoder(w).Encode(envelope)
}

func reasonStatus(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "ALREADY_EXISTS"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	}
	return "INTERNAL"
}

func writeDomainErr(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "backendError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchTable, domain.KindNoSuchItem:
		writeError(w, http.StatusNotFound, "notFound", de.Message)
	case domain.KindTableAlreadyExists:
		writeError(w, http.StatusConflict, "alreadyExists", de.Message)
	case domain.KindTableNotEmpty:
		writeError(w, http.StatusBadRequest, "failedPrecondition", de.Message)
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "invalid", de.Message)
	case domain.KindUnsupported:
		writeError(w, http.StatusBadRequest, "invalid", de.Message)
	default:
		writeError(w, http.StatusInternalServerError, "backendError", de.Message)
	}
}

// -------- json helpers --------

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
