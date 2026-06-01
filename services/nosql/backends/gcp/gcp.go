// Package gcp is the GCP Firestore Native passthrough backend. It
// uses google.golang.org/api/firestore/v1 to drive real Firestore
// (or a sockerless / per-request endpoint-overridden client for tests).
//
// **Stateless shim.** Every shim-managed datum lives in Firestore
// itself — including the per-table metadata Firestore Native doesn't
// natively support. Per N18 (docs/normalizations.md), the shim
// manufactures a `Table` abstraction over Firestore's no-tables
// model by storing each table's schema + tags + description in a
// reserved Firestore collection: `__shim_tables__/{tableName}`. The
// shim does not hold a name → doc map in process; every CreateTable
// reads/writes the metadata doc directly.
//
// **Document IDs encode composite keys.** Firestore documents are
// addressed by docID under a collection. The shim's domain.Key has
// `PartitionKey` + optional `SortKey`. The backend serialises a Key
// into a single docID using the same encoding pattern as the AWS
// page-token codec — typed prefixes (`s:` / `n:` / `b:` / `x:` /
// `_:`) joined by `|`, then base64-url-encoded so the result fits
// Firestore's docID character constraints.
//
// **Value translation (N19).** domain.Value → firestore.Value is:
// String → StringValue · Number (decimal string) → IntegerValue when
// int64-parseable, DoubleValue when float64-parseable with `.` or
// `e`, StringValue fallback for out-of-int64 integers · Bool →
// BooleanValue · Bytes → BytesValue (base64) · Null → NullValue.
// Composite types (List / Map / Set) reject at the boundary; the
// backend defends in depth even though the frontend already filters.
package gcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	firestore "google.golang.org/api/firestore/v1"
	"google.golang.org/api/googleapi"

	"github.com/e6qu/shimanism/internal/nosql/domain"
)

// Config selects which Firestore database + project the backend
// drives. Project / Database / Database location follow the
// normalisations contract (N6 region naming).
type Config struct {
	// ProjectID is the GCP project. Required.
	ProjectID string
	// DatabaseID is the Firestore database name. Defaults to
	// "(default)" if unset.
	DatabaseID string
}

// MetadataCollection is the reserved Firestore collection name the
// backend uses for per-table schema + tags + description. Per N18.
const MetadataCollection = "__shim_tables__"

// Backend implements domain.NoSQL via Firestore.
type Backend struct {
	c   *firestore.Service
	cfg Config
}

// New wraps an already-configured Firestore service.
func New(svc *firestore.Service, cfg Config) (*Backend, error) {
	if svc == nil {
		return nil, fmt.Errorf("firestore service required")
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("ProjectID required")
	}
	if cfg.DatabaseID == "" {
		cfg.DatabaseID = "(default)"
	}
	return &Backend{c: svc, cfg: cfg}, nil
}

var _ domain.NoSQL = (*Backend)(nil)

// ---------------- paths ----------------

// dbPath returns the parent path every documents.* call uses.
// `projects/{p}/databases/{db}/documents`.
func (b *Backend) dbPath() string {
	return fmt.Sprintf("projects/%s/databases/%s/documents", b.cfg.ProjectID, b.cfg.DatabaseID)
}

// docName composes the full document name for a (collection, docID)
// pair. `projects/{p}/databases/{db}/documents/{collection}/{docID}`.
func (b *Backend) docName(collection, docID string) string {
	return path.Join(b.dbPath(), collection, docID)
}

// metaName is shorthand for the metadata doc for a given table.
func (b *Backend) metaName(table string) string {
	return b.docName(MetadataCollection, table)
}

// itemName composes the full document name for an item key.
func (b *Backend) itemName(table string, key domain.Key) string {
	return b.docName(table, encodeKey(key))
}

// ---------------- key encoding ----------------

// encodeKey serialises a composite key into a Firestore-safe docID.
// Format: base64-url(typed-prefix-segments-joined-by-pipe).
func encodeKey(k domain.Key) string {
	raw := encodeValueAsDocSegment(k.PartitionKey)
	if k.SortKey.Type != domain.ValueUnknown {
		raw += "|" + encodeValueAsDocSegment(k.SortKey)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func encodeValueAsDocSegment(v domain.Value) string {
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
	return "_:unknown"
}

// describeKey is the human-readable form for error messages.
func describeKey(k domain.Key) string {
	parts := []string{readableValue(k.PartitionKey)}
	if k.SortKey.Type != domain.ValueUnknown {
		parts = append(parts, readableValue(k.SortKey))
	}
	return strings.Join(parts, "/")
}

func readableValue(v domain.Value) string {
	switch v.Type {
	case domain.ValueString:
		return v.Str
	case domain.ValueNumber:
		return v.Num
	case domain.ValueBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case domain.ValueBytes:
		return base64.StdEncoding.EncodeToString(v.Bin)
	case domain.ValueNull:
		return "null"
	}
	return ""
}

// ---------------- error translation ----------------

func translateErr(err error, ctx string) error {
	if err == nil {
		return nil
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusNotFound:
			return domain.NoSuchTable(ctx)
		case http.StatusConflict, http.StatusPreconditionFailed:
			return domain.TableAlreadyExists(ctx)
		case http.StatusBadRequest:
			return domain.InvalidArgument(apiErr.Message)
		}
	}
	return err
}

// ---------------- value translation (N19) ----------------

func valueToFS(v domain.Value) (*firestore.Value, error) {
	out := &firestore.Value{}
	switch v.Type {
	case domain.ValueString:
		out.StringValue = v.Str
		out.ForceSendFields = []string{"StringValue"}
		return out, nil
	case domain.ValueNumber:
		if n, err := strconv.ParseInt(v.Num, 10, 64); err == nil {
			out.IntegerValue = n
			out.ForceSendFields = []string{"IntegerValue"}
			return out, nil
		}
		if f, err := strconv.ParseFloat(v.Num, 64); err == nil && (strings.ContainsAny(v.Num, ".eE")) {
			out.DoubleValue = f
			out.ForceSendFields = []string{"DoubleValue"}
			return out, nil
		}
		// Out-of-int64 integer (e.g. "9999999999999999999") — fall
		// back to StringValue per N19's documented behaviour.
		out.StringValue = v.Num
		out.ForceSendFields = []string{"StringValue"}
		return out, nil
	case domain.ValueBool:
		out.BooleanValue = v.Bool
		out.ForceSendFields = []string{"BooleanValue"}
		return out, nil
	case domain.ValueBytes:
		out.BytesValue = base64.StdEncoding.EncodeToString(v.Bin)
		out.ForceSendFields = []string{"BytesValue"}
		return out, nil
	case domain.ValueNull:
		out.NullValue = "NULL_VALUE"
		out.ForceSendFields = []string{"NullValue"}
		return out, nil
	}
	return nil, domain.Unsupported("attribute value type " + v.Type.String())
}

func valueFromFS(v *firestore.Value) domain.Value {
	if v == nil {
		return domain.Value{}
	}
	switch {
	case v.StringValue != "" || hasField(v, "StringValue"):
		return domain.StringValue(v.StringValue)
	case v.IntegerValue != 0 || hasField(v, "IntegerValue"):
		return domain.NumberValue(strconv.FormatInt(v.IntegerValue, 10))
	case v.DoubleValue != 0 || hasField(v, "DoubleValue"):
		return domain.NumberValue(strconv.FormatFloat(v.DoubleValue, 'g', -1, 64))
	case v.BooleanValue || hasField(v, "BooleanValue"):
		return domain.BoolValue(v.BooleanValue)
	case v.BytesValue != "":
		b, _ := base64.StdEncoding.DecodeString(v.BytesValue)
		return domain.BytesValue(b)
	case v.NullValue == "NULL_VALUE":
		return domain.NullValue()
	}
	// Composite types (Array / Map / Reference / GeoPoint / Timestamp)
	// are out of intersection; surface as NullValue per the
	// frontend's filtering pattern.
	return domain.NullValue()
}

func hasField(v *firestore.Value, name string) bool {
	for _, f := range v.ForceSendFields {
		if f == name {
			return true
		}
	}
	return false
}

func attrsToFS(in map[string]domain.Value) (map[string]firestore.Value, error) {
	out := make(map[string]firestore.Value, len(in))
	for k, v := range in {
		fsv, err := valueToFS(v)
		if err != nil {
			return nil, err
		}
		out[k] = *fsv
	}
	return out, nil
}

func attrsFromFS(in map[string]firestore.Value) map[string]domain.Value {
	out := make(map[string]domain.Value, len(in))
	for k, v := range in {
		vv := v
		out[k] = valueFromFS(&vv)
	}
	return out
}

// ---------------- metadata ↔ Table ----------------

func tableToMetaDocument(name string, t domain.Table, created time.Time) *firestore.Document {
	fields := map[string]firestore.Value{
		"partitionKey": {StringValue: t.PartitionKeyName, ForceSendFields: []string{"StringValue"}},
		"sortKey":      {StringValue: t.SortKeyName, ForceSendFields: []string{"StringValue"}},
		"description":  {StringValue: t.Description, ForceSendFields: []string{"StringValue"}},
		"createdAt":    {TimestampValue: created.UTC().Format(time.RFC3339Nano), ForceSendFields: []string{"TimestampValue"}},
	}
	if len(t.Tags) > 0 {
		tagFields := map[string]firestore.Value{}
		for k, v := range t.Tags {
			tagFields[k] = firestore.Value{StringValue: v, ForceSendFields: []string{"StringValue"}}
		}
		fields["tags"] = firestore.Value{MapValue: &firestore.MapValue{Fields: tagFields}, ForceSendFields: []string{"MapValue"}}
	}
	return &firestore.Document{Fields: fields}
}

func tableFromMetaDocument(name string, doc *firestore.Document) domain.Table {
	t := domain.Table{Name: name}
	if doc == nil {
		return t
	}
	if v, ok := doc.Fields["partitionKey"]; ok {
		t.PartitionKeyName = v.StringValue
	}
	if v, ok := doc.Fields["sortKey"]; ok {
		t.SortKeyName = v.StringValue
	}
	if v, ok := doc.Fields["description"]; ok {
		t.Description = v.StringValue
	}
	if v, ok := doc.Fields["createdAt"]; ok && v.TimestampValue != "" {
		if ts, err := time.Parse(time.RFC3339Nano, v.TimestampValue); err == nil {
			t.CreatedAt = ts
		}
	}
	if v, ok := doc.Fields["tags"]; ok && v.MapValue != nil && len(v.MapValue.Fields) > 0 {
		t.Tags = map[string]string{}
		for k, vv := range v.MapValue.Fields {
			t.Tags[k] = vv.StringValue
		}
	}
	return t
}

// ---------------- Tables ----------------

func (b *Backend) CreateTable(ctx context.Context, name string, opt domain.CreateTableOptions) (domain.Table, error) {
	if name == "" {
		return domain.Table{}, domain.InvalidArgument("table name required")
	}
	if opt.PartitionKeyName == "" {
		return domain.Table{}, domain.InvalidArgument("partition key name required")
	}
	if name == MetadataCollection || strings.HasPrefix(name, "__") {
		return domain.Table{}, domain.InvalidArgument("table name reserved (shim metadata): " + name)
	}
	t := domain.Table{
		Name:             name,
		PartitionKeyName: opt.PartitionKeyName,
		SortKeyName:      opt.SortKeyName,
		Description:      opt.Description,
		Tags:             opt.Tags,
		CreatedAt:        time.Now().UTC(),
	}
	doc := tableToMetaDocument(name, t, t.CreatedAt)
	_, err := b.c.Projects.Databases.Documents.CreateDocument(b.dbPath(), MetadataCollection, doc).
		DocumentId(name).
		Context(ctx).Do()
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && (apiErr.Code == http.StatusConflict || apiErr.Code == http.StatusPreconditionFailed) {
			return domain.Table{}, domain.TableAlreadyExists(name)
		}
		return domain.Table{}, fmt.Errorf("create metadata doc: %w", err)
	}
	return t, nil
}

func (b *Backend) GetTable(ctx context.Context, name string) (domain.Table, error) {
	doc, err := b.c.Projects.Databases.Documents.Get(b.metaName(name)).Context(ctx).Do()
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			return domain.Table{}, domain.NoSuchTable(name)
		}
		return domain.Table{}, err
	}
	return tableFromMetaDocument(name, doc), nil
}

func (b *Backend) DeleteTable(ctx context.Context, name string, force bool) error {
	// Check items in {name} collection; refuse if non-empty + !force.
	listResp, err := b.c.Projects.Databases.Documents.List(b.dbPath(), name).PageSize(1).Context(ctx).Do()
	if err != nil {
		// Listing a non-existent collection returns an empty result on
		// Firestore (per the API surface), not 404 — so a real error
		// is a real error.
		return err
	}
	hasItems := len(listResp.Documents) > 0
	if !force && hasItems {
		return domain.TableNotEmpty(name)
	}
	if force && hasItems {
		// Drain in a loop. Firestore has no batch-delete-by-collection
		// primitive, so we list + delete in pages.
		pageToken := ""
		for {
			call := b.c.Projects.Databases.Documents.List(b.dbPath(), name).PageSize(100).Context(ctx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			resp, err := call.Do()
			if err != nil {
				return err
			}
			for _, d := range resp.Documents {
				if _, err := b.c.Projects.Databases.Documents.Delete(d.Name).Context(ctx).Do(); err != nil {
					// Best-effort drain; if one delete fails, keep going
					// and report the last error after the loop. But we
					// don't fabricate success — surface the error.
					return err
				}
			}
			if resp.NextPageToken == "" {
				break
			}
			pageToken = resp.NextPageToken
		}
	}
	_, err = b.c.Projects.Databases.Documents.Delete(b.metaName(name)).Context(ctx).Do()
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			return domain.NoSuchTable(name)
		}
		return err
	}
	return nil
}

func (b *Backend) ListTables(ctx context.Context, opt domain.ListTablesOptions) (domain.ListTablesResult, error) {
	res := domain.ListTablesResult{}
	call := b.c.Projects.Databases.Documents.List(b.dbPath(), MetadataCollection).Context(ctx)
	if opt.PageSize > 0 {
		call = call.PageSize(int64(opt.PageSize))
	}
	if opt.PageToken != "" {
		call = call.PageToken(opt.PageToken)
	}
	resp, err := call.Do()
	if err != nil {
		return res, err
	}
	for _, d := range resp.Documents {
		// Doc name = projects/.../documents/__shim_tables__/{tableName}.
		name := path.Base(d.Name)
		if opt.NamePrefix != "" && !strings.HasPrefix(name, opt.NamePrefix) {
			continue
		}
		res.Tables = append(res.Tables, tableFromMetaDocument(name, d))
	}
	res.NextPageToken = resp.NextPageToken
	return res, nil
}

func (b *Backend) UpdateTableTags(ctx context.Context, name string, tags map[string]string) error {
	t, err := b.GetTable(ctx, name)
	if err != nil {
		return err
	}
	t.Tags = tags
	// Patch the tags field. Use updateMask=tags so other fields
	// (partitionKey, sortKey, etc.) aren't touched.
	doc := tableToMetaDocument(name, t, t.CreatedAt)
	// Strip every field except tags from the request body — Firestore
	// patches only fields listed in updateMask, but it's cleaner to
	// only send what we're changing.
	doc.Fields = map[string]firestore.Value{"tags": doc.Fields["tags"]}
	// If tags ended up nil (no tags), include the field so the
	// updateMask actually clears it; use an empty MapValue.
	if doc.Fields["tags"].MapValue == nil {
		doc.Fields["tags"] = firestore.Value{MapValue: &firestore.MapValue{Fields: map[string]firestore.Value{}}, ForceSendFields: []string{"MapValue"}}
	}
	_, err = b.c.Projects.Databases.Documents.Patch(b.metaName(name), doc).
		UpdateMaskFieldPaths("tags").
		Context(ctx).Do()
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			return domain.NoSuchTable(name)
		}
		return err
	}
	return nil
}

// ---------------- Items ----------------

func (b *Backend) PutItem(ctx context.Context, table string, item domain.Item) error {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return err
	}
	pk, ok := item.Attributes[t.PartitionKeyName]
	if !ok || pk.Type == domain.ValueUnknown {
		return domain.InvalidArgument("item missing partition key attribute " + t.PartitionKeyName)
	}
	key := domain.Key{PartitionKey: pk}
	if t.SortKeyName != "" {
		sk, ok := item.Attributes[t.SortKeyName]
		if !ok || sk.Type == domain.ValueUnknown {
			return domain.InvalidArgument("item missing sort key attribute " + t.SortKeyName)
		}
		key.SortKey = sk
	}
	fields, err := attrsToFS(item.Attributes)
	if err != nil {
		return err
	}
	// Patch creates-or-replaces by docName.
	doc := &firestore.Document{Fields: fields}
	_, err = b.c.Projects.Databases.Documents.Patch(b.itemName(table, key), doc).Context(ctx).Do()
	return translateErr(err, table)
}

func (b *Backend) GetItem(ctx context.Context, table string, key domain.Key) (domain.Item, error) {
	if _, err := b.GetTable(ctx, table); err != nil {
		return domain.Item{}, err
	}
	doc, err := b.c.Projects.Databases.Documents.Get(b.itemName(table, key)).Context(ctx).Do()
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			return domain.Item{}, domain.NoSuchItem(table, describeKey(key))
		}
		return domain.Item{}, err
	}
	return domain.Item{Attributes: attrsFromFS(doc.Fields)}, nil
}

func (b *Backend) DeleteItem(ctx context.Context, table string, key domain.Key) error {
	if _, err := b.GetTable(ctx, table); err != nil {
		return err
	}
	_, err := b.c.Projects.Databases.Documents.Delete(b.itemName(table, key)).Context(ctx).Do()
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			return domain.NoSuchItem(table, describeKey(key))
		}
		return err
	}
	return nil
}

func (b *Backend) Scan(ctx context.Context, table string, opt domain.ScanOptions) (domain.ScanResult, error) {
	if _, err := b.GetTable(ctx, table); err != nil {
		return domain.ScanResult{}, err
	}
	call := b.c.Projects.Databases.Documents.List(b.dbPath(), table).Context(ctx)
	if opt.Limit > 0 {
		call = call.PageSize(int64(opt.Limit))
	}
	if opt.PageToken != "" {
		call = call.PageToken(opt.PageToken)
	}
	resp, err := call.Do()
	if err != nil {
		return domain.ScanResult{}, translateErr(err, table)
	}
	res := domain.ScanResult{NextPageToken: resp.NextPageToken}
	for _, d := range resp.Documents {
		res.Items = append(res.Items, domain.Item{Attributes: attrsFromFS(d.Fields)})
	}
	return res, nil
}

// Query enumerates items by partition key (with optional sort-key
// prefix). Because the Firestore REST runQuery endpoint streams a
// chunked array of RunQueryResponse messages and the Go
// google.golang.org/api/firestore/v1 generated client decodes only
// the first element, the shim cannot use the SDK's RunQuery to read
// every match in one call. Instead the backend uses documents.list
// (paginated single-shot JSON) and filters client-side. This is
// O(N) on collection size for Query — acceptable for the shim's
// fidelity-first stance and documented as the Firestore-specific
// trade-off. Sockerless's GCP sim implements the same REST surface.
func (b *Backend) Query(ctx context.Context, table string, pk domain.Value, opt domain.QueryOptions) (domain.QueryResult, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.QueryResult{}, err
	}
	if t.PartitionKeyName == "" {
		return domain.QueryResult{}, domain.InvalidArgument("table " + table + " has no partition key")
	}
	if opt.SortKeyPrefix != "" && t.SortKeyName == "" {
		return domain.QueryResult{}, domain.InvalidArgument("SortKeyPrefix supplied but table " + table + " has no sort key")
	}
	res := domain.QueryResult{}
	pageToken := opt.PageToken
	for {
		call := b.c.Projects.Databases.Documents.List(b.dbPath(), table).PageSize(100).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return domain.QueryResult{}, translateErr(err, table)
		}
		for _, d := range resp.Documents {
			attrs := attrsFromFS(d.Fields)
			pkAttr, ok := attrs[t.PartitionKeyName]
			if !ok || !valuesEqual(pkAttr, pk) {
				continue
			}
			if opt.SortKeyPrefix != "" {
				skAttr, ok := attrs[t.SortKeyName]
				if !ok || skAttr.Type != domain.ValueString || !strings.HasPrefix(skAttr.Str, opt.SortKeyPrefix) {
					continue
				}
			}
			res.Items = append(res.Items, domain.Item{Attributes: attrs})
			if opt.Limit > 0 && len(res.Items) >= opt.Limit {
				res.NextPageToken = resp.NextPageToken
				return res, nil
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return res, nil
}

// valuesEqual is a typed equality check that mirrors how the shim's
// composite-key encoding compares values.
func valuesEqual(a, b domain.Value) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case domain.ValueString:
		return a.Str == b.Str
	case domain.ValueNumber:
		return a.Num == b.Num
	case domain.ValueBool:
		return a.Bool == b.Bool
	case domain.ValueBytes:
		return string(a.Bin) == string(b.Bin)
	case domain.ValueNull:
		return true
	}
	return false
}
