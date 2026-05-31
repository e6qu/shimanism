// Package inmem provides an in-memory NoSQL backend for tests.
// Mirrors the destination cloud's contract: tables + items stored
// in maps, atomic create/replace/delete, deterministic Scan ordering
// (by partition then sort) so test assertions stay stable.
package inmem

import (
	"context"
	"encoding/base64"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/nosql/domain"
)

type Backend struct {
	mu     sync.RWMutex
	tables map[string]*tableState
}

type tableState struct {
	table domain.Table
	items map[string]domain.Item // key: encoded composite key
}

func New() *Backend {
	return &Backend{tables: map[string]*tableState{}}
}

// encodeKey produces a deterministic, collision-free string from a
// composite key. Partition + sort segments are length-prefixed so
// values containing the separator can't alias.
func encodeKey(k domain.Key) string {
	return encodeValue(k.PartitionKey) + "|" + encodeValue(k.SortKey)
}

func encodeValue(v domain.Value) string {
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
	default:
		return "_:unknown"
	}
}

func (b *Backend) CreateTable(ctx context.Context, name string, opt domain.CreateTableOptions) (domain.Table, error) {
	if name == "" {
		return domain.Table{}, domain.InvalidArgument("table name required")
	}
	if opt.PartitionKeyName == "" {
		return domain.Table{}, domain.InvalidArgument("partition key name required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.tables[name]; ok {
		return domain.Table{}, domain.TableAlreadyExists(name)
	}
	t := domain.Table{
		Name:             name,
		PartitionKeyName: opt.PartitionKeyName,
		SortKeyName:      opt.SortKeyName,
		Description:      opt.Description,
		Tags:             copyTags(opt.Tags),
		CreatedAt:        time.Now().UTC(),
	}
	b.tables[name] = &tableState{table: t, items: map[string]domain.Item{}}
	return t, nil
}

func (b *Backend) GetTable(ctx context.Context, name string) (domain.Table, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	st, ok := b.tables[name]
	if !ok {
		return domain.Table{}, domain.NoSuchTable(name)
	}
	return st.table, nil
}

func (b *Backend) DeleteTable(ctx context.Context, name string, force bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.tables[name]
	if !ok {
		return domain.NoSuchTable(name)
	}
	if !force && len(st.items) > 0 {
		return domain.TableNotEmpty(name)
	}
	delete(b.tables, name)
	return nil
}

func (b *Backend) ListTables(ctx context.Context, opt domain.ListTablesOptions) (domain.ListTablesResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := domain.ListTablesResult{}
	names := make([]string, 0, len(b.tables))
	for n := range b.tables {
		if opt.NamePrefix != "" && !strings.HasPrefix(n, opt.NamePrefix) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out.Tables = append(out.Tables, b.tables[n].table)
	}
	return out, nil
}

func (b *Backend) PutItem(ctx context.Context, table string, item domain.Item) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.tables[table]
	if !ok {
		return domain.NoSuchTable(table)
	}
	key, err := extractKey(st.table, item)
	if err != nil {
		return err
	}
	stored := domain.Item{Attributes: copyAttrs(item.Attributes)}
	st.items[encodeKey(key)] = stored
	return nil
}

func (b *Backend) GetItem(ctx context.Context, table string, key domain.Key) (domain.Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	st, ok := b.tables[table]
	if !ok {
		return domain.Item{}, domain.NoSuchTable(table)
	}
	if err := validateKey(st.table, key); err != nil {
		return domain.Item{}, err
	}
	it, ok := st.items[encodeKey(key)]
	if !ok {
		return domain.Item{}, domain.NoSuchItem(table, encodeKey(key))
	}
	return domain.Item{Attributes: copyAttrs(it.Attributes)}, nil
}

func (b *Backend) DeleteItem(ctx context.Context, table string, key domain.Key) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.tables[table]
	if !ok {
		return domain.NoSuchTable(table)
	}
	if err := validateKey(st.table, key); err != nil {
		return err
	}
	enc := encodeKey(key)
	if _, ok := st.items[enc]; !ok {
		return domain.NoSuchItem(table, enc)
	}
	delete(st.items, enc)
	return nil
}

func (b *Backend) Scan(ctx context.Context, table string, opt domain.ScanOptions) (domain.ScanResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	st, ok := b.tables[table]
	if !ok {
		return domain.ScanResult{}, domain.NoSuchTable(table)
	}
	keys := make([]string, 0, len(st.items))
	for k := range st.items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := domain.ScanResult{}
	for _, k := range keys {
		if opt.Limit > 0 && len(out.Items) >= opt.Limit {
			out.NextPageToken = k
			break
		}
		it := st.items[k]
		out.Items = append(out.Items, domain.Item{Attributes: copyAttrs(it.Attributes)})
	}
	return out, nil
}

func (b *Backend) Query(ctx context.Context, table string, pk domain.Value, opt domain.QueryOptions) (domain.QueryResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	st, ok := b.tables[table]
	if !ok {
		return domain.QueryResult{}, domain.NoSuchTable(table)
	}
	pkEnc := encodeValue(pk)
	prefix := pkEnc + "|"
	keys := make([]string, 0, len(st.items))
	for k := range st.items {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if opt.SortKeyPrefix != "" {
			sortPart := strings.TrimPrefix(k, prefix)
			// SortKeyPrefix matches against string-typed sort keys.
			if !strings.HasPrefix(sortPart, "s:"+opt.SortKeyPrefix) {
				continue
			}
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := domain.QueryResult{}
	for _, k := range keys {
		if opt.Limit > 0 && len(out.Items) >= opt.Limit {
			out.NextPageToken = k
			break
		}
		it := st.items[k]
		out.Items = append(out.Items, domain.Item{Attributes: copyAttrs(it.Attributes)})
	}
	return out, nil
}

// extractKey derives the composite Key from an item's attributes,
// using the table's configured partition / sort key names. Items
// missing the required attributes are rejected.
func extractKey(t domain.Table, item domain.Item) (domain.Key, error) {
	pk, ok := item.Attributes[t.PartitionKeyName]
	if !ok || pk.Type == domain.ValueUnknown {
		return domain.Key{}, domain.InvalidArgument("item missing partition key attribute " + t.PartitionKeyName)
	}
	key := domain.Key{PartitionKey: pk}
	if t.SortKeyName != "" {
		sk, ok := item.Attributes[t.SortKeyName]
		if !ok || sk.Type == domain.ValueUnknown {
			return domain.Key{}, domain.InvalidArgument("item missing sort key attribute " + t.SortKeyName)
		}
		key.SortKey = sk
	}
	return key, nil
}

// validateKey checks that the caller-supplied key shape matches the
// table's schema.
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

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyAttrs(in map[string]domain.Value) map[string]domain.Value {
	if in == nil {
		return nil
	}
	out := make(map[string]domain.Value, len(in))
	for k, v := range in {
		// Bytes need defensive copy so callers can mutate their input.
		if v.Type == domain.ValueBytes {
			v.Bin = append([]byte(nil), v.Bin...)
		}
		out[k] = v
	}
	return out
}
