// Package domain holds shimanism's neutral NoSQL key-value
// interface and types. The interface is the lingua franca between
// the three frontends (AWS DynamoDB, GCP Firestore Native, Azure
// Cosmos DB Table API) and the four backends (AWS / GCP / Azure
// / etcd) + the inmem testing backend.
//
// Phase 15.C scoping in docs/phase-15-cd-scoping.md.
//
// **Stateless.** No per-process maps. Tables + items live in the
// destination cloud (or the inmem backend's map for tests).
package domain

import (
	"context"
	"time"
)

// ValueType discriminates the in-intersection scalar types across
// DynamoDB AttributeValue / Firestore Value / Cosmos EDM. Only the
// types that round-trip through all three are listed.
type ValueType int

const (
	ValueUnknown ValueType = iota
	ValueString
	ValueNumber // decimal string; mirrors DynamoDB's wire encoding (arbitrary precision)
	ValueBool
	ValueBytes
	ValueNull
)

func (t ValueType) String() string {
	switch t {
	case ValueString:
		return "string"
	case ValueNumber:
		return "number"
	case ValueBool:
		return "bool"
	case ValueBytes:
		return "bytes"
	case ValueNull:
		return "null"
	default:
		return "unknown"
	}
}

// Value is the shim's neutral attribute value. Each backend
// translates this onto the destination cloud's native value type
// (DynamoDB AttributeValue, Firestore Value, Cosmos EdmType).
//
// Numbers are carried as decimal strings so that DynamoDB-precision
// (arbitrary) and Firestore/Cosmos Int64/Double values round-trip
// without truncation. Backends validate at write time per their
// destination's bounds; frontends return the source cloud's error
// envelope on overflow.
type Value struct {
	Type ValueType

	// Str is set when Type == ValueString.
	Str string
	// Num is set when Type == ValueNumber; decimal string.
	Num string
	// Bin is set when Type == ValueBytes.
	Bin []byte
	// Bool is set when Type == ValueBool.
	Bool bool
}

// StringValue / NumberValue / BoolValue / BytesValue / NullValue are
// constructors that keep the discriminator and payload in lockstep.
func StringValue(s string) Value { return Value{Type: ValueString, Str: s} }
func NumberValue(n string) Value { return Value{Type: ValueNumber, Num: n} }
func BoolValue(b bool) Value     { return Value{Type: ValueBool, Bool: b} }
func BytesValue(b []byte) Value  { return Value{Type: ValueBytes, Bin: append([]byte(nil), b...)} }
func NullValue() Value           { return Value{Type: ValueNull} }

// Key identifies an item within a table. PartitionKey is required;
// SortKey is optional (only used when the table was created with a
// SortKeyName). The attribute names are stored on Table (see
// PartitionKeyName / SortKeyName); Key carries only the values.
type Key struct {
	PartitionKey Value
	SortKey      Value // Type == ValueUnknown means absent
}

// Item is the user-visible row. Attributes always carries the
// partition-key (and sort-key, when present) attribute under their
// configured names — round-tripping matches the destination cloud's
// native shape. Implementations must not mutate Attributes.
type Item struct {
	Attributes map[string]Value
}

// Table describes a NoSQL key-value table in shimanism's neutral
// form. Backends translate to/from cloud-native (DynamoDB
// TableDescription, Firestore Database — see N18 in
// docs/normalizations.md for the no-tables-in-Firestore mapping —
// Cosmos Table).
type Table struct {
	// Name is the table's identifier. Per-cloud naming constraints
	// apply at the frontend / backend boundary.
	Name string

	// PartitionKeyName is the attribute name that carries the
	// partition (hash) key value on every item.
	PartitionKeyName string

	// SortKeyName, when non-empty, is the attribute name that
	// carries the sort (range) key value on every item.
	SortKeyName string

	// Description is a free-text label round-tripped via the
	// destination cloud's native description / labels field per N4.
	Description string

	// Tags map to AWS tags / GCP labels / Azure tags per N3.
	Tags map[string]string

	// CreatedAt is when the destination cloud reports the table was
	// created. Zero when the destination cloud doesn't expose a
	// creation timestamp.
	CreatedAt time.Time
}

// CreateTableOptions controls CreateTable.
type CreateTableOptions struct {
	PartitionKeyName string
	SortKeyName      string // empty for partition-key-only tables
	Description      string
	Tags             map[string]string
}

// ListTablesOptions controls ListTables pagination + filtering.
type ListTablesOptions struct {
	NamePrefix string
	PageSize   int
	PageToken  string
}

// ListTablesResult is the ListTables response.
type ListTablesResult struct {
	Tables        []Table
	NextPageToken string
}

// ScanOptions controls Scan pagination. No filtering — Scan returns
// every item up to Limit. Filter expressions are out-of-intersection.
type ScanOptions struct {
	Limit     int
	PageToken string
}

// ScanResult is the Scan response.
type ScanResult struct {
	Items         []Item
	NextPageToken string
}

// QueryOptions controls Query (items sharing a partition key).
// SortKeyPrefix optionally restricts to sort keys with the given
// string prefix. Numeric / boolean / bytes sort-key filtering is
// out-of-intersection for 15.C; document at N19 if extended.
type QueryOptions struct {
	Limit         int
	PageToken     string
	SortKeyPrefix string
}

// QueryResult is the Query response.
type QueryResult struct {
	Items         []Item
	NextPageToken string
}

// NoSQL is the interface every NoSQL backend implements. The shim's
// frontends (DynamoDB / Firestore / Cosmos Tables) translate
// cloud-native API calls into these methods. The shim's backends
// translate these into destination-cloud-native API calls.
type NoSQL interface {
	// CreateTable creates a new table.
	CreateTable(ctx context.Context, name string, opt CreateTableOptions) (Table, error)

	// GetTable returns the named table's current state.
	GetTable(ctx context.Context, name string) (Table, error)

	// DeleteTable removes the table. force=true drops any remaining
	// items; force=false fails with TableNotEmpty when items exist.
	DeleteTable(ctx context.Context, name string, force bool) error

	// UpdateTableTags replaces the table's tag set with the supplied
	// map. Callers wanting Add / Remove semantics (e.g. AWS
	// TagResource / UntagResource) read GetTable.Tags, merge, then
	// call UpdateTableTags. Per N3 the per-cloud backends enforce
	// label constraints (GCP-side) and report violations.
	UpdateTableTags(ctx context.Context, name string, tags map[string]string) error

	// ListTables enumerates tables, optionally filtered by prefix.
	ListTables(ctx context.Context, opt ListTablesOptions) (ListTablesResult, error)

	// PutItem creates-or-replaces the item at (table, item.Key).
	// The item's Attributes must include the partition-key
	// attribute (and sort-key attribute, when the table has one).
	PutItem(ctx context.Context, table string, item Item) error

	// GetItem returns the item identified by key, or NoSuchItem.
	GetItem(ctx context.Context, table string, key Key) (Item, error)

	// DeleteItem removes the item at key, or returns NoSuchItem.
	DeleteItem(ctx context.Context, table string, key Key) error

	// Scan returns every item in the table, up to opt.Limit, with
	// continuation tokens for paging.
	Scan(ctx context.Context, table string, opt ScanOptions) (ScanResult, error)

	// Query returns every item whose partition key matches, up to
	// opt.Limit, optionally filtered by SortKeyPrefix.
	Query(ctx context.Context, table string, partitionKey Value, opt QueryOptions) (QueryResult, error)
}
