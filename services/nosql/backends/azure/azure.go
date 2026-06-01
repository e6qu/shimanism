// Package azure is the Azure Cosmos DB Table API passthrough
// backend. It uses github.com/Azure/azure-sdk-for-go/sdk/data/aztables
// to drive real Cosmos Tables (or a sockerless / per-request
// endpoint-overridden client for tests).
//
// **N18 — per-table metadata.** Cosmos Tables has explicit
// CreateTable / DeleteTable lifecycle but no per-table schema
// surface — every entity carries its own attributes plus a fixed
// PartitionKey + RowKey pair. The shim's domain.Table records
// schema (PartitionKeyName + optional SortKeyName + tags +
// description); the backend stores that schema as a metadata
// entity in a reserved Cosmos table `__shim_tables__`. The
// metadata write lives in the destination cloud itself — no shim
// sidecar storage.
//
// **Cosmos requires string PartitionKey + RowKey.** Domain.Value
// supports String / Number / Bool / Bytes / Null. To carry the
// non-string types through Cosmos's string-only keys without
// losing type information, the backend encodes them with the same
// typed prefixes used by the AWS page-token codec
// (`s:` / `n:` / `b:` / `x:` / `_:`).
//
// **Value translation (N19).** Item attributes round-trip through
// Cosmos EDM types:
//
//	String  → wire string (no @odata.type annotation)
//	Number  → Int64 (when int64-parseable) or Double, with
//	          @odata.type = "Edm.Int64" / "Edm.Double" added
//	          alongside the wire field
//	Bool    → wire boolean
//	Bytes   → base64-encoded wire string with @odata.type = "Edm.Binary"
//	Null    → omitted entirely (Cosmos has no native null;
//	          callers that need null round-trip must use a string
//	          sentinel)
package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"

	"github.com/e6qu/shimanism/internal/nosql/domain"
)

// MetadataTable is the reserved Cosmos table name the backend uses
// for per-table schema + tags + description. Per N18.
const MetadataTable = "shimtables"

// MetadataPartition is the fixed PartitionKey value every metadata
// entity uses.
const MetadataPartition = "metadata"

// Backend implements domain.NoSQL via Azure Cosmos DB Tables.
type Backend struct {
	svc *aztables.ServiceClient
}

// New wraps an already-configured Cosmos Tables service client.
func New(svc *aztables.ServiceClient) *Backend { return &Backend{svc: svc} }

var _ domain.NoSQL = (*Backend)(nil)

// ensureMetadataTable lazily creates the reserved metadata table.
// Calling CreateTable on an existing Cosmos table returns 409 — we
// swallow that case so the call is idempotent.
func (b *Backend) ensureMetadataTable(ctx context.Context) error {
	_, err := b.svc.CreateTable(ctx, MetadataTable, nil)
	if err == nil {
		return nil
	}
	if isStatus(err, http.StatusConflict) {
		return nil
	}
	return err
}

func isStatus(err error, code int) bool {
	var resp *azcore.ResponseError
	return errors.As(err, &resp) && resp.StatusCode == code
}

// ---------------- Tables ----------------

func (b *Backend) CreateTable(ctx context.Context, name string, opt domain.CreateTableOptions) (domain.Table, error) {
	if name == "" {
		return domain.Table{}, domain.InvalidArgument("table name required")
	}
	if opt.PartitionKeyName == "" {
		return domain.Table{}, domain.InvalidArgument("partition key name required")
	}
	if name == MetadataTable || strings.HasPrefix(strings.ToLower(name), "shim") {
		return domain.Table{}, domain.InvalidArgument("table name reserved (shim metadata): " + name)
	}
	if _, err := b.svc.CreateTable(ctx, name, nil); err != nil {
		if isStatus(err, http.StatusConflict) {
			return domain.Table{}, domain.TableAlreadyExists(name)
		}
		return domain.Table{}, err
	}
	if err := b.ensureMetadataTable(ctx); err != nil {
		return domain.Table{}, err
	}
	t := domain.Table{
		Name:             name,
		PartitionKeyName: opt.PartitionKeyName,
		SortKeyName:      opt.SortKeyName,
		Description:      opt.Description,
		Tags:             opt.Tags,
		CreatedAt:        time.Now().UTC(),
	}
	metaJSON, err := encodeMetadataEntity(t)
	if err != nil {
		return domain.Table{}, err
	}
	if _, err := b.svc.NewClient(MetadataTable).AddEntity(ctx, metaJSON, nil); err != nil {
		// Race / leftover: if a previous CreateTable wrote the
		// metadata but the user retried after a transient failure,
		// 409 here is benign — upsert instead.
		if isStatus(err, http.StatusConflict) {
			if _, err2 := b.svc.NewClient(MetadataTable).UpsertEntity(ctx, metaJSON, nil); err2 != nil {
				return domain.Table{}, err2
			}
		} else {
			return domain.Table{}, err
		}
	}
	return t, nil
}

func (b *Backend) GetTable(ctx context.Context, name string) (domain.Table, error) {
	if err := b.ensureMetadataTable(ctx); err != nil {
		return domain.Table{}, err
	}
	resp, err := b.svc.NewClient(MetadataTable).GetEntity(ctx, MetadataPartition, name, nil)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return domain.Table{}, domain.NoSuchTable(name)
		}
		return domain.Table{}, err
	}
	t, err := decodeMetadataEntity(resp.Value)
	if err != nil {
		return domain.Table{}, err
	}
	t.Name = name
	return t, nil
}

func (b *Backend) DeleteTable(ctx context.Context, name string, force bool) error {
	client := b.svc.NewClient(name)
	pager := client.NewListEntitiesPager(&aztables.ListEntitiesOptions{Top: int32Ptr(1)})
	if !force && pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isStatus(err, http.StatusNotFound) {
				return domain.NoSuchTable(name)
			}
			return err
		}
		if len(page.Entities) > 0 {
			return domain.TableNotEmpty(name)
		}
	}
	if force {
		// Drain all entities.
		drainPager := client.NewListEntitiesPager(nil)
		for drainPager.More() {
			page, err := drainPager.NextPage(ctx)
			if err != nil {
				if isStatus(err, http.StatusNotFound) {
					return domain.NoSuchTable(name)
				}
				return err
			}
			for _, raw := range page.Entities {
				var ent map[string]any
				if err := json.Unmarshal(raw, &ent); err != nil {
					return err
				}
				pk, _ := ent["PartitionKey"].(string)
				rk, _ := ent["RowKey"].(string)
				if _, err := client.DeleteEntity(ctx, pk, rk, nil); err != nil {
					return err
				}
			}
		}
	}
	if _, err := b.svc.DeleteTable(ctx, name, nil); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return domain.NoSuchTable(name)
		}
		return err
	}
	// Best-effort metadata cleanup; failure is non-fatal because the
	// table itself is gone.
	_, _ = b.svc.NewClient(MetadataTable).DeleteEntity(ctx, MetadataPartition, name, nil)
	return nil
}

func (b *Backend) ListTables(ctx context.Context, opt domain.ListTablesOptions) (domain.ListTablesResult, error) {
	if err := b.ensureMetadataTable(ctx); err != nil {
		return domain.ListTablesResult{}, err
	}
	res := domain.ListTablesResult{}
	pager := b.svc.NewClient(MetadataTable).NewListEntitiesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListTablesResult{}, err
		}
		for _, raw := range page.Entities {
			t, err := decodeMetadataEntity(raw)
			if err != nil {
				return domain.ListTablesResult{}, err
			}
			if opt.NamePrefix != "" && !strings.HasPrefix(t.Name, opt.NamePrefix) {
				continue
			}
			res.Tables = append(res.Tables, t)
		}
	}
	return res, nil
}

func (b *Backend) UpdateTableTags(ctx context.Context, name string, tags map[string]string) error {
	t, err := b.GetTable(ctx, name)
	if err != nil {
		return err
	}
	t.Tags = tags
	metaJSON, err := encodeMetadataEntity(t)
	if err != nil {
		return err
	}
	_, err = b.svc.NewClient(MetadataTable).UpsertEntity(ctx, metaJSON, nil)
	return err
}

// ---------------- Items ----------------

func (b *Backend) PutItem(ctx context.Context, table string, item domain.Item) error {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return err
	}
	entityJSON, err := buildEntityJSON(t, item)
	if err != nil {
		return err
	}
	_, err = b.svc.NewClient(table).UpsertEntity(ctx, entityJSON, nil)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return domain.NoSuchTable(table)
		}
		return err
	}
	return nil
}

func (b *Backend) GetItem(ctx context.Context, table string, key domain.Key) (domain.Item, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.Item{}, err
	}
	pkStr, rkStr, err := keyToCosmosStrings(t, key)
	if err != nil {
		return domain.Item{}, err
	}
	resp, err := b.svc.NewClient(table).GetEntity(ctx, pkStr, rkStr, nil)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return domain.Item{}, domain.NoSuchItem(table, pkStr+"/"+rkStr)
		}
		return domain.Item{}, err
	}
	attrs, err := decodeEntityToAttrs(t, resp.Value)
	if err != nil {
		return domain.Item{}, err
	}
	return domain.Item{Attributes: attrs}, nil
}

func (b *Backend) DeleteItem(ctx context.Context, table string, key domain.Key) error {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return err
	}
	pkStr, rkStr, err := keyToCosmosStrings(t, key)
	if err != nil {
		return err
	}
	_, err = b.svc.NewClient(table).DeleteEntity(ctx, pkStr, rkStr, nil)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return domain.NoSuchItem(table, pkStr+"/"+rkStr)
		}
		return err
	}
	return nil
}

func (b *Backend) Scan(ctx context.Context, table string, opt domain.ScanOptions) (domain.ScanResult, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.ScanResult{}, err
	}
	list := &aztables.ListEntitiesOptions{}
	if opt.Limit > 0 {
		list.Top = int32Ptr(int32(opt.Limit))
	}
	pager := b.svc.NewClient(table).NewListEntitiesPager(list)
	res := domain.ScanResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ScanResult{}, err
		}
		for _, raw := range page.Entities {
			attrs, err := decodeEntityToAttrs(t, raw)
			if err != nil {
				return domain.ScanResult{}, err
			}
			res.Items = append(res.Items, domain.Item{Attributes: attrs})
			if opt.Limit > 0 && len(res.Items) >= opt.Limit {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) Query(ctx context.Context, table string, pk domain.Value, opt domain.QueryOptions) (domain.QueryResult, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.QueryResult{}, err
	}
	pkStr := encodeValueAsKey(pk)
	filter := "PartitionKey eq '" + escapeODataLiteral(pkStr) + "'"
	if opt.SortKeyPrefix != "" {
		if t.SortKeyName == "" {
			return domain.QueryResult{}, domain.InvalidArgument("SortKeyPrefix supplied but table " + table + " has no sort key")
		}
		// Cosmos OData supports ge / lt; emulate prefix via [prefix, prefix + "￿").
		filter += " and RowKey ge '" + escapeODataLiteral("s:"+opt.SortKeyPrefix) + "'"
		filter += " and RowKey lt '" + escapeODataLiteral("s:"+opt.SortKeyPrefix+"￿") + "'"
	}
	list := &aztables.ListEntitiesOptions{Filter: &filter}
	if opt.Limit > 0 {
		list.Top = int32Ptr(int32(opt.Limit))
	}
	pager := b.svc.NewClient(table).NewListEntitiesPager(list)
	res := domain.QueryResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.QueryResult{}, err
		}
		for _, raw := range page.Entities {
			attrs, err := decodeEntityToAttrs(t, raw)
			if err != nil {
				return domain.QueryResult{}, err
			}
			res.Items = append(res.Items, domain.Item{Attributes: attrs})
			if opt.Limit > 0 && len(res.Items) >= opt.Limit {
				return res, nil
			}
		}
	}
	return res, nil
}

// ---------------- key encoding ----------------

// encodeValueAsKey converts a domain.Value to a string suitable for
// Cosmos's PartitionKey / RowKey columns. Strings pass through with
// an "s:" prefix so the decoder knows the original type; other
// types use the typed-prefix encoding pattern. Empty string is
// represented as "s:" (still legal per Cosmos's key constraints).
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

func decodeKeyValue(s string) domain.Value {
	if len(s) < 2 || s[1] != ':' {
		// Best-effort: treat as raw string. Real Cosmos keys written
		// outside the shim won't have the typed prefix.
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

func keyToCosmosStrings(t domain.Table, k domain.Key) (string, string, error) {
	if k.PartitionKey.Type == domain.ValueUnknown {
		return "", "", domain.InvalidArgument("partition key required")
	}
	pk := encodeValueAsKey(k.PartitionKey)
	rk := ""
	if t.SortKeyName != "" {
		if k.SortKey.Type == domain.ValueUnknown {
			return "", "", domain.InvalidArgument("table " + t.Name + " requires a sort key")
		}
		rk = encodeValueAsKey(k.SortKey)
	} else {
		if k.SortKey.Type != domain.ValueUnknown {
			return "", "", domain.InvalidArgument("table " + t.Name + " has no sort key but one was supplied")
		}
		// Cosmos requires a non-empty RowKey; use a fixed sentinel.
		rk = "_:none"
	}
	return pk, rk, nil
}

// escapeODataLiteral doubles single-quotes per OData v3 escaping.
func escapeODataLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// ---------------- entity ↔ item ----------------

// buildEntityJSON serialises a domain.Item into the JSON Cosmos
// expects on insert/upsert. It enforces N19 typing via @odata.type
// annotations.
func buildEntityJSON(t domain.Table, item domain.Item) ([]byte, error) {
	pkAttr, ok := item.Attributes[t.PartitionKeyName]
	if !ok || pkAttr.Type == domain.ValueUnknown {
		return nil, domain.InvalidArgument("item missing partition key attribute " + t.PartitionKeyName)
	}
	rkAttr := domain.Value{}
	if t.SortKeyName != "" {
		sk, ok := item.Attributes[t.SortKeyName]
		if !ok || sk.Type == domain.ValueUnknown {
			return nil, domain.InvalidArgument("item missing sort key attribute " + t.SortKeyName)
		}
		rkAttr = sk
	}
	out := map[string]any{
		"PartitionKey": encodeValueAsKey(pkAttr),
		"RowKey":       encodeValueAsKey(rkAttr),
	}
	if t.SortKeyName == "" {
		out["RowKey"] = "_:none"
	}
	for k, v := range item.Attributes {
		if k == t.PartitionKeyName || k == t.SortKeyName {
			continue
		}
		setEntityField(out, k, v)
	}
	return json.Marshal(out)
}

func setEntityField(out map[string]any, k string, v domain.Value) {
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
			// Out-of-int64 integer fallback: keep as string per N19.
			out[k] = v.Num
		}
	case domain.ValueBool:
		out[k] = v.Bool
	case domain.ValueBytes:
		out[k] = base64.StdEncoding.EncodeToString(v.Bin)
		out[k+"@odata.type"] = "Edm.Binary"
	case domain.ValueNull:
		// Cosmos has no native null. Omit the field.
	}
}

// decodeEntityToAttrs parses Cosmos's JSON entity body back into a
// domain.Item's attributes, using the schema to invert the
// PartitionKey/RowKey columns to the user's named attributes.
func decodeEntityToAttrs(t domain.Table, raw []byte) (map[string]domain.Value, error) {
	var ent map[string]any
	if err := json.Unmarshal(raw, &ent); err != nil {
		return nil, err
	}
	attrs := map[string]domain.Value{}
	for k, v := range ent {
		if k == "PartitionKey" {
			s, _ := v.(string)
			attrs[t.PartitionKeyName] = decodeKeyValue(s)
			continue
		}
		if k == "RowKey" {
			s, _ := v.(string)
			if t.SortKeyName != "" {
				attrs[t.SortKeyName] = decodeKeyValue(s)
			}
			continue
		}
		// Skip Azure-side metadata + odata annotations.
		if k == "Timestamp" || k == "etag" || strings.Contains(k, "@odata") || strings.HasPrefix(k, "odata.") {
			continue
		}
		// Look up the EDM type annotation, if any.
		edmAnnotation, _ := ent[k+"@odata.type"].(string)
		attrs[k] = decodeEntityValue(v, edmAnnotation)
	}
	return attrs, nil
}

func decodeEntityValue(raw any, edm string) domain.Value {
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
		// JSON number → store as decimal string to preserve precision.
		// If the value is an integer, prefer integer notation.
		if x == float64(int64(x)) {
			return domain.NumberValue(strconv.FormatInt(int64(x), 10))
		}
		return domain.NumberValue(strconv.FormatFloat(x, 'g', -1, 64))
	case nil:
		return domain.NullValue()
	}
	return domain.NullValue()
}

// ---------------- metadata entity ----------------

func encodeMetadataEntity(t domain.Table) ([]byte, error) {
	ent := map[string]any{
		"PartitionKey": MetadataPartition,
		"RowKey":       t.Name,
		"partitionKey": t.PartitionKeyName,
		"sortKey":      t.SortKeyName,
		"description":  t.Description,
	}
	if !t.CreatedAt.IsZero() {
		ent["createdAt"] = t.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if len(t.Tags) > 0 {
		// Cosmos has no nested map type; serialise tags as a JSON
		// string and decode on read.
		tagsJSON, err := json.Marshal(t.Tags)
		if err != nil {
			return nil, err
		}
		ent["tags"] = string(tagsJSON)
	}
	return json.Marshal(ent)
}

func decodeMetadataEntity(raw []byte) (domain.Table, error) {
	var ent map[string]any
	if err := json.Unmarshal(raw, &ent); err != nil {
		return domain.Table{}, err
	}
	t := domain.Table{}
	if v, ok := ent["RowKey"].(string); ok {
		t.Name = v
	}
	if v, ok := ent["partitionKey"].(string); ok {
		t.PartitionKeyName = v
	}
	if v, ok := ent["sortKey"].(string); ok {
		t.SortKeyName = v
	}
	if v, ok := ent["description"].(string); ok {
		t.Description = v
	}
	if v, ok := ent["createdAt"].(string); ok && v != "" {
		if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
			t.CreatedAt = ts
		}
	}
	if v, ok := ent["tags"].(string); ok && v != "" {
		tags := map[string]string{}
		if err := json.Unmarshal([]byte(v), &tags); err == nil {
			t.Tags = tags
		}
	}
	return t, nil
}

// ---------------- helpers ----------------

func int32Ptr(i int32) *int32 { return &i }

// debug helper kept for diagnostic prints during development.
var _ = fmt.Sprintf
