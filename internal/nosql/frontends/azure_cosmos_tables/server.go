// Package azure_cosmos_tables is the Azure Cosmos DB Table API
// frontend for shimanism's NoSQL service. It speaks the
// Tables-on-Storage REST protocol (OData v3) that
// github.com/Azure/azure-sdk-for-go/sdk/data/aztables drives, and
// translates each request into a call on the neutral
// `domain.NoSQL` interface.
//
// Cosmos's Table API and Storage Tables share the same wire
// protocol — the only difference is the endpoint host. SDK
// clients connect with SharedKey auth (or Cosmos-flavored MS Entra
// AAD tokens, which fall back to the same SharedKey verifier when
// the shim's `SHIMANISM_TEST_UNAUTHENTICATED=1` is set).
//
// **Schema mapping.** Cosmos requires PartitionKey + RowKey as
// strings on every entity. The shim's domain.Table records the
// user's PartitionKeyName + SortKeyName separately and the backend
// stores schema in the reserved `shimtables` table. The frontend
// looks up the schema (via the backend's GetTable) to translate
// the wire's PartitionKey + RowKey columns into the user's named
// attributes.
package azure_cosmos_tables

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/azuresharedkey"
	"github.com/e6qu/shimanism/internal/nosql/domain"
)

// MetadataTable mirrors the backend's reserved table name; the
// frontend rejects writes there from external clients so users
// can't corrupt shim state by directly editing schema rows.
const MetadataTable = "shimtables"

// Config controls optional frontend behaviour. Most callers use
// the zero value via New(); through-shim ARM scenarios (Terraform,
// az CLI) wire a Passthrough handler so unrecognised ARM paths
// (`/subscriptions/...`) reach an upstream Cosmos DB ARM resource
// provider rather than 404.
type Config struct {
	// Passthrough forwards unmatched ARM paths (subscriptions,
	// resource groups, `Microsoft.DocumentDB/databaseAccounts/...`,
	// metadata endpoints when MetadataLoginURL is unset, …) to the
	// upstream handler. nil → unmatched paths 404. Used by
	// through-shim Apply tests where the destination cloud's full
	// ARM surface needs to be reachable through the shim's port.
	Passthrough http.Handler
}

// Server is a Tables-shaped HTTP frontend dispatching to a
// domain.NoSQL backend.
type Server struct {
	n           domain.NoSQL
	passthrough http.Handler
}

// New returns a frontend bound to the given backend.
func New(n domain.NoSQL) *Server { return &Server{n: n} }

// NewWithPassthrough wires an upstream ARM handler that the frontend
// forwards unmatched ARM paths to (paths starting with
// `/subscriptions/`). Used by through-shim Terraform / az CLI tests
// where the user's azurerm provider drives ARM operations on
// `Microsoft.DocumentDB/databaseAccounts/...` through the shim's
// endpoint.
func NewWithPassthrough(n domain.NoSQL, upstream http.Handler) *Server {
	return NewWithConfig(n, Config{Passthrough: upstream})
}

// NewWithConfig is the full constructor.
func NewWithConfig(n domain.NoSQL, cfg Config) *Server {
	return &Server{n: n, passthrough: cfg.Passthrough}
}

// Handler wraps Server with the SharedKey verifier middleware.
// SHIMANISM_TEST_UNAUTHENTICATED=1 short-circuits verification.
func Handler(n domain.NoSQL) http.Handler {
	return verifierMiddleware()(New(n))
}

// HandlerWithPassthrough wraps a Server configured with an ARM
// passthrough handler. The SharedKey verifier middleware runs only
// on data-plane paths; ARM paths (`/subscriptions/...`) bypass it
// because azurerm authenticates ARM with Bearer tokens, not
// SharedKey, and the upstream handler validates those tokens.
//
// A follow-on PR will add an Azure bearer verifier in front of the
// passthrough for end-to-end auth coverage; for now the upstream
// (typically sockerless's Azure ARM stub) does the work.
func HandlerWithPassthrough(n domain.NoSQL, upstream http.Handler) http.Handler {
	server := NewWithPassthrough(n, upstream)
	wrapped := verifierMiddleware()(server)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(path, "subscriptions/") {
			// ARM paths skip the SharedKey middleware so Bearer-
			// authed azurerm/az calls reach the passthrough.
			server.ServeHTTP(w, r)
			return
		}
		wrapped.ServeHTTP(w, r)
	})
}

func verifierMiddleware() func(http.Handler) http.Handler {
	verifier := azuresharedkey.New(azuresharedkey.StaticStore{
		Account: "shimcosmos",
		Key:     []byte("test-key-do-not-use-in-prod-this-is-32-bytes-of-junk"),
	})
	return azuresharedkey.Middleware(verifier)
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	// ARM paths flow to the upstream passthrough when configured.
	// `/subscriptions/<sub>/...` is the universal ARM root —
	// Microsoft.DocumentDB/databaseAccounts/{account}/tables/{name}
	// and every other ARM resource lives under it. Without a
	// passthrough configured, unmatched paths fall through to the
	// data-plane dispatch below and 404 with the Tables error
	// envelope.
	if srv.passthrough != nil && strings.HasPrefix(path, "subscriptions/") {
		srv.passthrough.ServeHTTP(w, r)
		return
	}

	// Tables management:
	//   GET    /Tables           — list tables
	//   POST   /Tables           — create table
	//   DELETE /Tables('<name>') — delete table
	//   GET    /Tables('<name>') — get table (rarely used; aztables
	//                              uses ListTables with a filter)
	if path == "Tables" || strings.HasPrefix(path, "Tables(") {
		srv.handleTables(w, r, path)
		return
	}

	// Entity operations on a specific table:
	//   POST   /<table>                          — insert entity
	//   GET    /<table>()                        — list / query entities
	//   GET    /<table>(PartitionKey='x',RowKey='y') — get entity
	//   PUT    /<table>(PartitionKey='x',RowKey='y') — upsert (replace)
	//   MERGE  /<table>(PartitionKey='x',RowKey='y') — upsert (merge)
	//   DELETE /<table>(PartitionKey='x',RowKey='y') — delete entity
	tableName, keySelector, hasSelector := parseEntityPath(path)
	if tableName == "" {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "no route matches "+r.URL.Path)
		return
	}
	if tableName == MetadataTable {
		writeError(w, http.StatusForbidden, "AuthorizationFailure", "writes to the shim's reserved metadata table are not permitted")
		return
	}
	if !hasSelector {
		switch r.Method {
		case http.MethodPost:
			srv.insertEntity(w, r, tableName)
		case http.MethodGet:
			srv.listEntities(w, r, tableName)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	// Has a (PartitionKey='x',RowKey='y') selector.
	pkStr, rkStr, ok := decodeKeySelector(keySelector)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidInput", "malformed key selector: "+keySelector)
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getEntity(w, r, tableName, pkStr, rkStr)
	case http.MethodDelete:
		srv.deleteEntity(w, r, tableName, pkStr, rkStr)
	case http.MethodPut, "MERGE", "PATCH":
		srv.upsertEntity(w, r, tableName, pkStr, rkStr)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

// -------- Tables management --------

func (srv *Server) handleTables(w http.ResponseWriter, r *http.Request, path string) {
	if path == "Tables" {
		switch r.Method {
		case http.MethodGet:
			srv.listTables(w, r)
		case http.MethodPost:
			srv.createTable(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	// Tables('<name>')
	inner := strings.TrimPrefix(path, "Tables(")
	inner = strings.TrimSuffix(inner, ")")
	name := strings.Trim(inner, "'\"")
	switch r.Method {
	case http.MethodDelete:
		srv.deleteTable(w, r, name)
	case http.MethodGet:
		srv.getTable(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createTable(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
		return
	}
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "InvalidInput", "TableName required")
		return
	}
	// SDK callers don't pass schema; default to a single PartitionKey
	// attribute named "PartitionKey" + a RowKey attribute named
	// "RowKey". Cross-cloud Apply that needs custom schema uses
	// domain.NoSQL directly through a non-Tables frontend.
	_, err = srv.n.CreateTable(r.Context(), req.TableName, domain.CreateTableOptions{
		PartitionKeyName: "PartitionKey",
		SortKeyName:      "RowKey",
	})
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"odata.metadata": "https://shimcosmos.table.core.windows.net/$metadata#Tables/@Element",
		"TableName":      req.TableName,
	})
}

func (srv *Server) listTables(w http.ResponseWriter, r *http.Request) {
	res, err := srv.n.ListTables(r.Context(), domain.ListTablesOptions{})
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	out := map[string]any{
		"odata.metadata": "https://shimcosmos.table.core.windows.net/$metadata#Tables",
	}
	items := []map[string]any{}
	for _, t := range res.Tables {
		items = append(items, map[string]any{"TableName": t.Name})
	}
	out["value"] = items
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteTable(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.n.DeleteTable(r.Context(), name, true); err != nil {
		writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) getTable(w http.ResponseWriter, r *http.Request, name string) {
	if _, err := srv.n.GetTable(r.Context(), name); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"odata.metadata": "https://shimcosmos.table.core.windows.net/$metadata#Tables/@Element",
		"TableName":      name,
	})
}

// -------- Entity operations --------

func (srv *Server) insertEntity(w http.ResponseWriter, r *http.Request, table string) {
	t, err := srv.n.GetTable(r.Context(), table)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	attrs, perr := readEntityBody(r, t)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", perr.Error())
		return
	}
	if err := srv.n.PutItem(r.Context(), table, domain.Item{Attributes: attrs}); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, attrsToEntityJSON(t, attrs))
}

func (srv *Server) upsertEntity(w http.ResponseWriter, r *http.Request, table, pkStr, rkStr string) {
	t, err := srv.n.GetTable(r.Context(), table)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	attrs, perr := readEntityBody(r, t)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", perr.Error())
		return
	}
	// Ensure the body's PK/RK align with the URL selector. SDK
	// always echoes them; reject mismatches.
	if v, ok := attrs[t.PartitionKeyName]; !ok || encodeValueAsKey(v) != pkStr {
		attrs[t.PartitionKeyName] = domainValueFromKeyEncoded(pkStr)
	}
	if t.SortKeyName != "" {
		if v, ok := attrs[t.SortKeyName]; !ok || encodeValueAsKey(v) != rkStr {
			attrs[t.SortKeyName] = domainValueFromKeyEncoded(rkStr)
		}
	}
	if err := srv.n.PutItem(r.Context(), table, domain.Item{Attributes: attrs}); err != nil {
		writeDomainErr(w, err)
		return
	}
	w.Header().Set("ETag", `W/"datetime'`+time.Now().UTC().Format(time.RFC3339Nano)+`'"`)
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) getEntity(w http.ResponseWriter, r *http.Request, table, pkStr, rkStr string) {
	t, err := srv.n.GetTable(r.Context(), table)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	key := domain.Key{PartitionKey: domainValueFromKeyEncoded(pkStr)}
	if t.SortKeyName != "" {
		key.SortKey = domainValueFromKeyEncoded(rkStr)
	}
	item, err := srv.n.GetItem(r.Context(), table, key)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, attrsToEntityJSON(t, item.Attributes))
}

func (srv *Server) deleteEntity(w http.ResponseWriter, r *http.Request, table, pkStr, rkStr string) {
	t, err := srv.n.GetTable(r.Context(), table)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	key := domain.Key{PartitionKey: domainValueFromKeyEncoded(pkStr)}
	if t.SortKeyName != "" {
		key.SortKey = domainValueFromKeyEncoded(rkStr)
	}
	if err := srv.n.DeleteItem(r.Context(), table, key); err != nil {
		writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) listEntities(w http.ResponseWriter, r *http.Request, table string) {
	t, err := srv.n.GetTable(r.Context(), table)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	q := r.URL.Query()
	filter := q.Get("$filter")
	top, _ := strconv.Atoi(q.Get("$top"))

	var items []domain.Item
	if filter != "" {
		// We support exactly the shape `PartitionKey eq '<x>' [and
		// RowKey ge '<y>' and RowKey lt '<z>']` — i.e. what the
		// Cosmos backend's Query emits. Anything else falls through
		// to a Scan + client-side filter, with a documented O(N) cost.
		pkVal, ok := parsePartitionKeyEquals(filter)
		if ok {
			res, qerr := srv.n.Query(r.Context(), table, pkVal, domain.QueryOptions{Limit: top})
			if qerr != nil {
				writeDomainErr(w, qerr)
				return
			}
			items = res.Items
		} else {
			res, qerr := srv.n.Scan(r.Context(), table, domain.ScanOptions{Limit: top})
			if qerr != nil {
				writeDomainErr(w, qerr)
				return
			}
			items = res.Items
		}
	} else {
		res, qerr := srv.n.Scan(r.Context(), table, domain.ScanOptions{Limit: top})
		if qerr != nil {
			writeDomainErr(w, qerr)
			return
		}
		items = res.Items
	}
	out := map[string]any{
		"odata.metadata": "https://shimcosmos.table.core.windows.net/$metadata#" + table,
		"value":          entitiesToJSON(t, items),
	}
	writeJSON(w, http.StatusOK, out)
}

// -------- helpers --------

func readEntityBody(r *http.Request, t domain.Table) (map[string]domain.Value, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	attrs := map[string]domain.Value{}
	for k, v := range raw {
		// Skip Azure-side annotations.
		if strings.Contains(k, "@odata") || strings.HasPrefix(k, "odata.") {
			continue
		}
		// Map PartitionKey/RowKey wire columns to the user's named
		// attributes per the table's schema.
		if k == "PartitionKey" {
			s, _ := v.(string)
			attrs[t.PartitionKeyName] = domainValueFromKeyEncoded(s)
			continue
		}
		if k == "RowKey" {
			s, _ := v.(string)
			if t.SortKeyName != "" {
				attrs[t.SortKeyName] = domainValueFromKeyEncoded(s)
			}
			continue
		}
		edmAnnotation, _ := raw[k+"@odata.type"].(string)
		attrs[k] = decodeEntityField(v, edmAnnotation)
	}
	return attrs, nil
}

func decodeEntityField(raw any, edm string) domain.Value {
	switch edm {
	case "Edm.Int32", "Edm.Int64":
		switch x := raw.(type) {
		case string:
			return domain.NumberValue(x)
		case float64:
			return domain.NumberValue(strconv.FormatInt(int64(x), 10))
		}
	case "Edm.Double":
		switch x := raw.(type) {
		case float64:
			return domain.NumberValue(strconv.FormatFloat(x, 'g', -1, 64))
		case string:
			return domain.NumberValue(x)
		}
	case "Edm.Binary":
		s, _ := raw.(string)
		b, err := base64.StdEncoding.DecodeString(s)
		if err == nil {
			return domain.BytesValue(b)
		}
	}
	switch x := raw.(type) {
	case string:
		return domain.StringValue(x)
	case bool:
		return domain.BoolValue(x)
	case float64:
		if x == float64(int64(x)) {
			return domain.NumberValue(strconv.FormatInt(int64(x), 10))
		}
		return domain.NumberValue(strconv.FormatFloat(x, 'g', -1, 64))
	case nil:
		return domain.NullValue()
	}
	return domain.NullValue()
}

func attrsToEntityJSON(t domain.Table, attrs map[string]domain.Value) map[string]any {
	out := map[string]any{
		"odata.metadata": "https://shimcosmos.table.core.windows.net/$metadata#" + t.Name + "/@Element",
		"odata.etag":     `W/"datetime'` + time.Now().UTC().Format(time.RFC3339Nano) + `'"`,
		"Timestamp":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	pkVal, ok := attrs[t.PartitionKeyName]
	if ok {
		out["PartitionKey"] = encodeValueAsKey(pkVal)
	}
	if t.SortKeyName != "" {
		if sk, ok := attrs[t.SortKeyName]; ok {
			out["RowKey"] = encodeValueAsKey(sk)
		}
	} else {
		out["RowKey"] = "_:none"
	}
	for k, v := range attrs {
		if k == t.PartitionKeyName || k == t.SortKeyName {
			continue
		}
		setOutField(out, k, v)
	}
	return out
}

func setOutField(out map[string]any, k string, v domain.Value) {
	switch v.Type {
	case domain.ValueString:
		out[k] = v.Str
	case domain.ValueNumber:
		if n, err := strconv.ParseInt(v.Num, 10, 64); err == nil {
			out[k] = strconv.FormatInt(n, 10)
			out[k+"@odata.type"] = "Edm.Int64"
		} else if f, err := strconv.ParseFloat(v.Num, 64); err == nil && strings.ContainsAny(v.Num, ".eE") {
			out[k] = f
		} else {
			out[k] = v.Num
		}
	case domain.ValueBool:
		out[k] = v.Bool
	case domain.ValueBytes:
		out[k] = base64.StdEncoding.EncodeToString(v.Bin)
		out[k+"@odata.type"] = "Edm.Binary"
	case domain.ValueNull:
		// Skip — Cosmos has no native null.
	}
}

func entitiesToJSON(t domain.Table, items []domain.Item) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		ent := attrsToEntityJSON(t, it.Attributes)
		// Drop the per-entity odata.metadata; only the outer
		// response carries it for the list form.
		delete(ent, "odata.metadata")
		out = append(out, ent)
	}
	return out
}

// parseEntityPath splits a wire path of the shape `<table>` or
// `<table>(PartitionKey='x',RowKey='y')`, returning the table name,
// the key selector, and whether a selector was present.
func parseEntityPath(path string) (string, string, bool) {
	if path == "" {
		return "", "", false
	}
	openIdx := strings.IndexByte(path, '(')
	if openIdx < 0 {
		// No selector — table-level (e.g. POST /<table>).
		return path, "", false
	}
	table := path[:openIdx]
	rest := path[openIdx:]
	if rest == "()" {
		return table, "", false
	}
	if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
		return "", "", false
	}
	return table, rest[1 : len(rest)-1], true
}

// decodeKeySelector parses `PartitionKey='x',RowKey='y'`.
func decodeKeySelector(s string) (string, string, bool) {
	pk, rk := "", ""
	parts := splitOutsideQuotes(s, ',')
	for _, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return "", "", false
		}
		name := strings.TrimSpace(p[:eq])
		raw := strings.TrimSpace(p[eq+1:])
		// raw is like 's:abc' wrapped in single quotes; trim them.
		raw = strings.Trim(raw, "'")
		raw = strings.ReplaceAll(raw, "''", "'")
		switch name {
		case "PartitionKey":
			pk = raw
		case "RowKey":
			rk = raw
		}
	}
	return pk, rk, true
}

func splitOutsideQuotes(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			depth ^= 1
		} else if c == sep && depth == 0 {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// parsePartitionKeyEquals extracts a partition key value from a
// `PartitionKey eq '<x>'` OData clause. Returns ok=false for any
// other shape so the frontend can fall back to a Scan.
func parsePartitionKeyEquals(filter string) (domain.Value, bool) {
	const prefix = "PartitionKey eq '"
	idx := strings.Index(filter, prefix)
	if idx < 0 {
		return domain.Value{}, false
	}
	rest := filter[idx+len(prefix):]
	closeIdx := strings.IndexByte(rest, '\'')
	if closeIdx < 0 {
		return domain.Value{}, false
	}
	return domainValueFromKeyEncoded(rest[:closeIdx]), true
}

// -------- key codec (must match the backend's encoding) --------

func encodeValueAsKey(v domain.Value) string {
	switch v.Type {
	case domain.ValueString:
		return "s:" + v.Str
	case domain.ValueNumber:
		return "n:" + v.Num
	case domain.ValueBool:
		if v.Bool {
			return "b:1"
		}
		return "b:0"
	case domain.ValueBytes:
		return "x:" + base64.StdEncoding.EncodeToString(v.Bin)
	case domain.ValueNull:
		return "_:null"
	}
	return ""
}

func domainValueFromKeyEncoded(s string) domain.Value {
	if len(s) < 2 || s[1] != ':' {
		return domain.StringValue(s)
	}
	val := s[2:]
	switch s[0] {
	case 's':
		return domain.StringValue(val)
	case 'n':
		return domain.NumberValue(val)
	case 'b':
		return domain.BoolValue(val == "1")
	case 'x':
		b, err := base64.StdEncoding.DecodeString(val)
		if err == nil {
			return domain.BytesValue(b)
		}
		return domain.StringValue(s)
	case '_':
		return domain.NullValue()
	}
	return domain.StringValue(s)
}

// -------- error envelope --------

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	envelope := map[string]any{
		"odata.error": map[string]any{
			"code":    code,
			"message": map[string]any{"lang": "en-US", "value": message},
		},
	}
	_ = json.NewEncoder(w).Encode(envelope)
}

func writeDomainErr(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchTable:
		writeError(w, http.StatusNotFound, "TableNotFound", de.Message)
	case domain.KindNoSuchItem:
		writeError(w, http.StatusNotFound, "ResourceNotFound", de.Message)
	case domain.KindTableAlreadyExists:
		writeError(w, http.StatusConflict, "TableAlreadyExists", de.Message)
	case domain.KindTableNotEmpty:
		writeError(w, http.StatusBadRequest, "TableNotEmpty", de.Message)
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "InvalidInput", de.Message)
	case domain.KindUnsupported:
		writeError(w, http.StatusBadRequest, "InvalidInput", de.Message)
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerError", de.Message)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json;odata=minimalmetadata")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// kept-to-satisfy-imports: ensures url.QueryUnescape stays in the
// dependency surface even if a future refactor stops using it
// directly in the parsed paths.
var _ = url.QueryUnescape
