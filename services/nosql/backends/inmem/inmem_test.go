package inmem

import (
	"context"
	"testing"

	"github.com/e6qu/shimanism/internal/nosql/domain"
)

func TestTableLifecycle(t *testing.T) {
	ctx := context.Background()
	b := New()

	tbl, err := b.CreateTable(ctx, "users", domain.CreateTableOptions{
		PartitionKeyName: "id",
		Description:      "users table",
		Tags:             map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if tbl.PartitionKeyName != "id" || tbl.SortKeyName != "" {
		t.Errorf("schema mismatch: %+v", tbl)
	}

	if _, err := b.CreateTable(ctx, "users", domain.CreateTableOptions{PartitionKeyName: "id"}); !domain.IsKind(err, domain.KindTableAlreadyExists) {
		t.Errorf("CreateTable duplicate = %v, want TableAlreadyExists", err)
	}

	got, err := b.GetTable(ctx, "users")
	if err != nil || got.Description != "users table" || got.Tags["env"] != "test" {
		t.Errorf("GetTable round-trip = %+v, err=%v", got, err)
	}

	if err := b.DeleteTable(ctx, "users", false); err != nil {
		t.Errorf("DeleteTable empty: %v", err)
	}
	if _, err := b.GetTable(ctx, "users"); !domain.IsKind(err, domain.KindNoSuchTable) {
		t.Errorf("GetTable after delete = %v, want NoSuchTable", err)
	}
}

func TestPutGetDelete_PartitionOnly(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustCreate(t, b, "kv", domain.CreateTableOptions{PartitionKeyName: "id"})

	item := domain.Item{Attributes: map[string]domain.Value{
		"id":   domain.StringValue("a"),
		"data": domain.NumberValue("42"),
	}}
	if err := b.PutItem(ctx, "kv", item); err != nil {
		t.Fatalf("PutItem: %v", err)
	}
	got, err := b.GetItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("a")})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Attributes["data"].Num != "42" {
		t.Errorf("round-trip Num = %q, want 42", got.Attributes["data"].Num)
	}

	// Mutate input map after Put; backend should hold a defensive copy.
	item.Attributes["data"] = domain.NumberValue("99")
	got2, _ := b.GetItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("a")})
	if got2.Attributes["data"].Num != "42" {
		t.Errorf("backend stored shared reference; got Num=%q after caller mutation", got2.Attributes["data"].Num)
	}

	if err := b.DeleteItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("a")}); err != nil {
		t.Errorf("DeleteItem: %v", err)
	}
	if _, err := b.GetItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("a")}); !domain.IsKind(err, domain.KindNoSuchItem) {
		t.Errorf("GetItem after delete = %v, want NoSuchItem", err)
	}
}

func TestPutItem_MissingPartitionKey(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustCreate(t, b, "kv", domain.CreateTableOptions{PartitionKeyName: "id"})

	item := domain.Item{Attributes: map[string]domain.Value{
		"data": domain.StringValue("orphan"),
	}}
	err := b.PutItem(ctx, "kv", item)
	if !domain.IsKind(err, domain.KindInvalidArgument) {
		t.Errorf("PutItem missing PK = %v, want InvalidArgument", err)
	}
}

func TestPutGetDelete_CompositeKey(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustCreate(t, b, "events", domain.CreateTableOptions{
		PartitionKeyName: "user",
		SortKeyName:      "ts",
	})

	items := []domain.Item{
		{Attributes: map[string]domain.Value{"user": domain.StringValue("u1"), "ts": domain.StringValue("2026-01"), "msg": domain.StringValue("a")}},
		{Attributes: map[string]domain.Value{"user": domain.StringValue("u1"), "ts": domain.StringValue("2026-02"), "msg": domain.StringValue("b")}},
		{Attributes: map[string]domain.Value{"user": domain.StringValue("u2"), "ts": domain.StringValue("2026-01"), "msg": domain.StringValue("c")}},
	}
	for _, it := range items {
		if err := b.PutItem(ctx, "events", it); err != nil {
			t.Fatalf("PutItem: %v", err)
		}
	}

	// Missing sort key on a composite-key table is rejected.
	_, err := b.GetItem(ctx, "events", domain.Key{PartitionKey: domain.StringValue("u1")})
	if !domain.IsKind(err, domain.KindInvalidArgument) {
		t.Errorf("GetItem without sort key = %v, want InvalidArgument", err)
	}

	got, err := b.GetItem(ctx, "events", domain.Key{
		PartitionKey: domain.StringValue("u1"),
		SortKey:      domain.StringValue("2026-02"),
	})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Attributes["msg"].Str != "b" {
		t.Errorf("composite GetItem msg = %q, want b", got.Attributes["msg"].Str)
	}
}

func TestScan_PaginatesAndOrders(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustCreate(t, b, "kv", domain.CreateTableOptions{PartitionKeyName: "id"})
	for _, id := range []string{"c", "a", "b"} {
		mustPut(t, b, "kv", domain.Item{Attributes: map[string]domain.Value{"id": domain.StringValue(id)}})
	}

	res, err := b.Scan(ctx, "kv", domain.ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("Scan len = %d, want 3", len(res.Items))
	}
	ids := []string{res.Items[0].Attributes["id"].Str, res.Items[1].Attributes["id"].Str, res.Items[2].Attributes["id"].Str}
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("Scan order = %v, want [a b c]", ids)
	}

	page, err := b.Scan(ctx, "kv", domain.ScanOptions{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextPageToken == "" {
		t.Errorf("Scan paginated = %+v, err=%v", page, err)
	}
}

func TestQuery_PartitionAndPrefix(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustCreate(t, b, "events", domain.CreateTableOptions{
		PartitionKeyName: "user",
		SortKeyName:      "ts",
	})
	mustPut(t, b, "events", domain.Item{Attributes: map[string]domain.Value{"user": domain.StringValue("u1"), "ts": domain.StringValue("2026-01-01")}})
	mustPut(t, b, "events", domain.Item{Attributes: map[string]domain.Value{"user": domain.StringValue("u1"), "ts": domain.StringValue("2026-01-02")}})
	mustPut(t, b, "events", domain.Item{Attributes: map[string]domain.Value{"user": domain.StringValue("u1"), "ts": domain.StringValue("2026-02-01")}})
	mustPut(t, b, "events", domain.Item{Attributes: map[string]domain.Value{"user": domain.StringValue("u2"), "ts": domain.StringValue("2026-01-01")}})

	all, err := b.Query(ctx, "events", domain.StringValue("u1"), domain.QueryOptions{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(all.Items) != 3 {
		t.Errorf("Query u1 len = %d, want 3", len(all.Items))
	}

	jan, err := b.Query(ctx, "events", domain.StringValue("u1"), domain.QueryOptions{SortKeyPrefix: "2026-01"})
	if err != nil {
		t.Fatalf("Query prefix: %v", err)
	}
	if len(jan.Items) != 2 {
		t.Errorf("Query u1 prefix=2026-01 len = %d, want 2", len(jan.Items))
	}
}

func TestDeleteTable_NotEmpty(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustCreate(t, b, "kv", domain.CreateTableOptions{PartitionKeyName: "id"})
	mustPut(t, b, "kv", domain.Item{Attributes: map[string]domain.Value{"id": domain.StringValue("x")}})

	if err := b.DeleteTable(ctx, "kv", false); !domain.IsKind(err, domain.KindTableNotEmpty) {
		t.Errorf("DeleteTable force=false on non-empty = %v, want TableNotEmpty", err)
	}
	if err := b.DeleteTable(ctx, "kv", true); err != nil {
		t.Errorf("DeleteTable force=true: %v", err)
	}
}

func TestValueRoundTrip_AllTypes(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustCreate(t, b, "v", domain.CreateTableOptions{PartitionKeyName: "id"})

	cases := []struct {
		name string
		val  domain.Value
	}{
		{"string", domain.StringValue("hello")},
		{"number", domain.NumberValue("3.14159265358979323846")},
		{"bool_true", domain.BoolValue(true)},
		{"bool_false", domain.BoolValue(false)},
		{"bytes", domain.BytesValue([]byte{0xde, 0xad, 0xbe, 0xef})},
		{"null", domain.NullValue()},
	}
	for _, c := range cases {
		mustPut(t, b, "v", domain.Item{Attributes: map[string]domain.Value{
			"id":  domain.StringValue(c.name),
			"val": c.val,
		}})
		got, err := b.GetItem(ctx, "v", domain.Key{PartitionKey: domain.StringValue(c.name)})
		if err != nil {
			t.Fatalf("%s GetItem: %v", c.name, err)
		}
		stored := got.Attributes["val"]
		if stored.Type != c.val.Type {
			t.Errorf("%s Type = %s, want %s", c.name, stored.Type, c.val.Type)
		}
		// Spot-check payload by type.
		switch c.val.Type {
		case domain.ValueString:
			if stored.Str != c.val.Str {
				t.Errorf("%s Str = %q, want %q", c.name, stored.Str, c.val.Str)
			}
		case domain.ValueNumber:
			if stored.Num != c.val.Num {
				t.Errorf("%s Num = %q, want %q", c.name, stored.Num, c.val.Num)
			}
		case domain.ValueBool:
			if stored.Bool != c.val.Bool {
				t.Errorf("%s Bool = %v, want %v", c.name, stored.Bool, c.val.Bool)
			}
		case domain.ValueBytes:
			if string(stored.Bin) != string(c.val.Bin) {
				t.Errorf("%s Bin = %x, want %x", c.name, stored.Bin, c.val.Bin)
			}
		}
	}
}

// ---- helpers ----

func mustCreate(t *testing.T, b *Backend, name string, opt domain.CreateTableOptions) {
	t.Helper()
	if _, err := b.CreateTable(context.Background(), name, opt); err != nil {
		t.Fatalf("CreateTable %s: %v", name, err)
	}
}

func mustPut(t *testing.T, b *Backend, table string, item domain.Item) {
	t.Helper()
	if err := b.PutItem(context.Background(), table, item); err != nil {
		t.Fatalf("PutItem: %v", err)
	}
}
