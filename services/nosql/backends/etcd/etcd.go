// Package etcd is shimanism's K8s peer for NoSQL: an etcd-backed
// implementation of domain.NoSQL, the canonical "fourth backend"
// per AGENTS.md's "Kubernetes is the fourth backend, always" rule.
//
// **Stateless shim.** Every shim-managed datum lives in etcd
// itself. Per-table schema goes to `__shim_tables__/<name>`; each
// item lives at `<table>/items/<encoded-key>`. No name → key map
// in process; lookups go straight to etcd.
//
// **Key layout.**
//
//	__shim_tables__/<table-name>           — table metadata (JSON)
//	<table-name>/items/<encoded-key>       — item attributes (JSON)
//
// `<encoded-key>` is the same typed-prefix encoding used by the
// GCP Firestore + Azure Cosmos backends:
// base64-url(`<typed-segment>[|<typed-segment>]`) where each segment
// is `s:<str>` / `n:<num>` / `b:0|1` / `x:<base64>` / `_:null`. This
// preserves cross-cloud Apply round-tripping — items written by an
// AWS frontend through the etcd backend can be read by any other
// frontend.
//
// **Value translation (N19).** Every attribute is stored as a JSON
// fragment under its name. `domain.Value` round-trips via the
// typed-discriminator encoding:
//
//	{"type":"string","str":"..."} / {"type":"number","num":"..."} /
//	{"type":"bool","bool":...} / {"type":"bytes","bin":"<base64>"} /
//	{"type":"null"}
//
// JSON keeps the encoding inspectable in `etcdctl get`; binary
// encoding would be smaller but less debuggable.
package etcd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/e6qu/shimanism/internal/nosql/domain"
)

// MetadataPrefix is the reserved key prefix the backend uses for
// per-table schema + tags + description. Per N18.
const MetadataPrefix = "__shim_tables__/"

// Backend implements domain.NoSQL via an etcd cluster.
type Backend struct {
	c *clientv3.Client
}

// New wraps an already-configured etcd client.
func New(client *clientv3.Client) *Backend { return &Backend{c: client} }

var _ domain.NoSQL = (*Backend)(nil)

// ---------------- keys ----------------

func metaKey(table string) string { return MetadataPrefix + table }

func itemPrefix(table string) string { return table + "/items/" }

func itemKey(table, encodedKey string) string {
	return itemPrefix(table) + encodedKey
}

// encodeKey serialises a composite Key into an etcd-safe segment.
// Format: base64-url(typed-segments-joined-by-pipe). Matches the GCP
// and Azure backends so cross-cloud Apply round-trips.
func encodeKey(k domain.Key) string {
	raw := encodeValueAsSegment(k.PartitionKey)
	if k.SortKey.Type != domain.ValueUnknown {
		raw += "|" + encodeValueAsSegment(k.SortKey)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func encodeValueAsSegment(v domain.Value) string {
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

// ---------------- value translation (N19) ----------------

type jsonValue struct {
	Type string `json:"type"`
	Str  string `json:"str,omitempty"`
	Num  string `json:"num,omitempty"`
	Bool bool   `json:"bool,omitempty"`
	Bin  []byte `json:"bin,omitempty"`
}

func valueToJSON(v domain.Value) jsonValue {
	switch v.Type {
	case domain.ValueString:
		return jsonValue{Type: "string", Str: v.Str}
	case domain.ValueNumber:
		return jsonValue{Type: "number", Num: v.Num}
	case domain.ValueBool:
		return jsonValue{Type: "bool", Bool: v.Bool}
	case domain.ValueBytes:
		return jsonValue{Type: "bytes", Bin: append([]byte(nil), v.Bin...)}
	case domain.ValueNull:
		return jsonValue{Type: "null"}
	}
	return jsonValue{Type: "unknown"}
}

func valueFromJSON(j jsonValue) domain.Value {
	switch j.Type {
	case "string":
		return domain.StringValue(j.Str)
	case "number":
		return domain.NumberValue(j.Num)
	case "bool":
		return domain.BoolValue(j.Bool)
	case "bytes":
		return domain.BytesValue(j.Bin)
	case "null":
		return domain.NullValue()
	}
	return domain.NullValue()
}

func attrsToJSON(in map[string]domain.Value) ([]byte, error) {
	m := make(map[string]jsonValue, len(in))
	for k, v := range in {
		m[k] = valueToJSON(v)
	}
	return json.Marshal(m)
}

func attrsFromJSON(raw []byte) (map[string]domain.Value, error) {
	m := map[string]jsonValue{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make(map[string]domain.Value, len(m))
	for k, j := range m {
		out[k] = valueFromJSON(j)
	}
	return out, nil
}

// ---------------- metadata ↔ Table ----------------

type metaJSON struct {
	PartitionKeyName string            `json:"partitionKey"`
	SortKeyName      string            `json:"sortKey,omitempty"`
	Description      string            `json:"description,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
}

func tableToMetadata(t domain.Table) ([]byte, error) {
	return json.Marshal(metaJSON{
		PartitionKeyName: t.PartitionKeyName,
		SortKeyName:      t.SortKeyName,
		Description:      t.Description,
		Tags:             t.Tags,
		CreatedAt:        t.CreatedAt,
	})
}

func tableFromMetadata(name string, raw []byte) (domain.Table, error) {
	var m metaJSON
	if err := json.Unmarshal(raw, &m); err != nil {
		return domain.Table{}, fmt.Errorf("decode metadata for %s: %w", name, err)
	}
	return domain.Table{
		Name:             name,
		PartitionKeyName: m.PartitionKeyName,
		SortKeyName:      m.SortKeyName,
		Description:      m.Description,
		Tags:             m.Tags,
		CreatedAt:        m.CreatedAt,
	}, nil
}

// ---------------- Tables ----------------

func (b *Backend) CreateTable(ctx context.Context, name string, opt domain.CreateTableOptions) (domain.Table, error) {
	if name == "" {
		return domain.Table{}, domain.InvalidArgument("table name required")
	}
	if opt.PartitionKeyName == "" {
		return domain.Table{}, domain.InvalidArgument("partition key name required")
	}
	if strings.HasPrefix(name, "__") {
		return domain.Table{}, domain.InvalidArgument("table name reserved (shim metadata prefix): " + name)
	}
	t := domain.Table{
		Name:             name,
		PartitionKeyName: opt.PartitionKeyName,
		SortKeyName:      opt.SortKeyName,
		Description:      opt.Description,
		Tags:             opt.Tags,
		CreatedAt:        time.Now().UTC(),
	}
	body, err := tableToMetadata(t)
	if err != nil {
		return domain.Table{}, err
	}
	// Create-only: use a transaction with a "key does not exist"
	// guard so duplicates surface as TableAlreadyExists rather than
	// being silently overwritten.
	key := metaKey(name)
	resp, err := b.c.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
		Then(clientv3.OpPut(key, string(body))).
		Commit()
	if err != nil {
		return domain.Table{}, err
	}
	if !resp.Succeeded {
		return domain.Table{}, domain.TableAlreadyExists(name)
	}
	return t, nil
}

func (b *Backend) GetTable(ctx context.Context, name string) (domain.Table, error) {
	resp, err := b.c.Get(ctx, metaKey(name))
	if err != nil {
		return domain.Table{}, err
	}
	if len(resp.Kvs) == 0 {
		return domain.Table{}, domain.NoSuchTable(name)
	}
	return tableFromMetadata(name, resp.Kvs[0].Value)
}

func (b *Backend) DeleteTable(ctx context.Context, name string, force bool) error {
	if _, err := b.GetTable(ctx, name); err != nil {
		return err
	}
	if !force {
		resp, err := b.c.Get(ctx, itemPrefix(name), clientv3.WithPrefix(), clientv3.WithLimit(1))
		if err != nil {
			return err
		}
		if len(resp.Kvs) > 0 {
			return domain.TableNotEmpty(name)
		}
	}
	if force {
		// Best-effort drain by prefix.
		if _, err := b.c.Delete(ctx, itemPrefix(name), clientv3.WithPrefix()); err != nil {
			return err
		}
	}
	if _, err := b.c.Delete(ctx, metaKey(name)); err != nil {
		return err
	}
	return nil
}

func (b *Backend) ListTables(ctx context.Context, opt domain.ListTablesOptions) (domain.ListTablesResult, error) {
	// etcd's range over __shim_tables__/ gives every table's meta.
	resp, err := b.c.Get(ctx, MetadataPrefix,
		clientv3.WithPrefix(),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
	)
	if err != nil {
		return domain.ListTablesResult{}, err
	}
	res := domain.ListTablesResult{}
	for _, kv := range resp.Kvs {
		name := strings.TrimPrefix(string(kv.Key), MetadataPrefix)
		if opt.NamePrefix != "" && !strings.HasPrefix(name, opt.NamePrefix) {
			continue
		}
		t, err := tableFromMetadata(name, kv.Value)
		if err != nil {
			return domain.ListTablesResult{}, err
		}
		res.Tables = append(res.Tables, t)
	}
	return res, nil
}

func (b *Backend) UpdateTableTags(ctx context.Context, name string, tags map[string]string) error {
	t, err := b.GetTable(ctx, name)
	if err != nil {
		return err
	}
	t.Tags = tags
	body, err := tableToMetadata(t)
	if err != nil {
		return err
	}
	// Conditional put: only overwrite if the key still exists (i.e.
	// the table wasn't deleted concurrently). Surfaces as NoSuchTable
	// if a race wins.
	key := metaKey(name)
	resp, err := b.c.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(key), "!=", 0)).
		Then(clientv3.OpPut(key, string(body))).
		Commit()
	if err != nil {
		return err
	}
	if !resp.Succeeded {
		return domain.NoSuchTable(name)
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
	body, err := attrsToJSON(item.Attributes)
	if err != nil {
		return err
	}
	_, err = b.c.Put(ctx, itemKey(table, encodeKey(key)), string(body))
	return err
}

func (b *Backend) GetItem(ctx context.Context, table string, key domain.Key) (domain.Item, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.Item{}, err
	}
	if err := validateKey(t, key); err != nil {
		return domain.Item{}, err
	}
	resp, err := b.c.Get(ctx, itemKey(table, encodeKey(key)))
	if err != nil {
		return domain.Item{}, err
	}
	if len(resp.Kvs) == 0 {
		return domain.Item{}, domain.NoSuchItem(table, describeKey(key))
	}
	attrs, err := attrsFromJSON(resp.Kvs[0].Value)
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
	if err := validateKey(t, key); err != nil {
		return err
	}
	resp, err := b.c.Delete(ctx, itemKey(table, encodeKey(key)))
	if err != nil {
		return err
	}
	if resp.Deleted == 0 {
		return domain.NoSuchItem(table, describeKey(key))
	}
	return nil
}

func (b *Backend) Scan(ctx context.Context, table string, opt domain.ScanOptions) (domain.ScanResult, error) {
	if _, err := b.GetTable(ctx, table); err != nil {
		return domain.ScanResult{}, err
	}
	// Range bounds: [start, end) where end is computed by
	// clientv3.GetPrefixRangeEnd on the table's items prefix. When a
	// PageToken is present it overrides `start` (the token is
	// already the next-key-after-the-last-returned, so the same end
	// still applies).
	prefix := itemPrefix(table)
	start := prefix
	if opt.PageToken != "" {
		start = opt.PageToken
	}
	end := clientv3.GetPrefixRangeEnd(prefix)
	opts := []clientv3.OpOption{
		clientv3.WithRange(end),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
	}
	if opt.Limit > 0 {
		opts = append(opts, clientv3.WithLimit(int64(opt.Limit)))
	}
	resp, err := b.c.Get(ctx, start, opts...)
	if err != nil {
		return domain.ScanResult{}, err
	}
	res := domain.ScanResult{}
	for _, kv := range resp.Kvs {
		attrs, err := attrsFromJSON(kv.Value)
		if err != nil {
			return domain.ScanResult{}, err
		}
		res.Items = append(res.Items, domain.Item{Attributes: attrs})
	}
	if resp.More && len(resp.Kvs) > 0 {
		// Continuation token = "next key after the last returned".
		// Appending a nul byte produces the lexicographically
		// smallest key strictly greater than the last; etcd's range
		// reads the next batch starting at that key.
		last := string(resp.Kvs[len(resp.Kvs)-1].Key)
		res.NextPageToken = last + "\x00"
	}
	return res, nil
}

func (b *Backend) Query(ctx context.Context, table string, pk domain.Value, opt domain.QueryOptions) (domain.QueryResult, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.QueryResult{}, err
	}
	if opt.SortKeyPrefix != "" && t.SortKeyName == "" {
		return domain.QueryResult{}, domain.InvalidArgument("SortKeyPrefix supplied but table " + table + " has no sort key")
	}
	// We can't compute the etcd prefix from the partition key alone
	// because the composite key encodes BOTH pk and sk into a single
	// base64 string. So we range-scan the table's items and filter
	// client-side. The shim's domain.Query semantics are
	// equality-on-partition + optional prefix-on-sort, which can be
	// computed from the decoded attributes.
	resp, err := b.c.Get(ctx, itemPrefix(table),
		clientv3.WithPrefix(),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
	)
	if err != nil {
		return domain.QueryResult{}, err
	}
	res := domain.QueryResult{}
	for _, kv := range resp.Kvs {
		attrs, err := attrsFromJSON(kv.Value)
		if err != nil {
			return domain.QueryResult{}, err
		}
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
			break
		}
	}
	return res, nil
}

// ---------------- helpers ----------------

func validateKey(t domain.Table, k domain.Key) error {
	if k.PartitionKey.Type == domain.ValueUnknown {
		return domain.InvalidArgument("partition key required")
	}
	if t.SortKeyName == "" && k.SortKey.Type != domain.ValueUnknown {
		return domain.InvalidArgument("table " + t.Name + " has no sort key but one was supplied")
	}
	if t.SortKeyName != "" && k.SortKey.Type == domain.ValueUnknown {
		return domain.InvalidArgument("table " + t.Name + " requires a sort key")
	}
	return nil
}

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

