// Live conformance: start a real `etcd` server, point a clientv3 at
// it, drive the backend through table + item lifecycle + every
// value type + composite-key Query + Scan pagination. Validates the
// end-to-end serialization the K8s peer's "fourth backend" slot
// depends on.
//
// Skipped if the `etcd` binary isn't on PATH (CI provisions it via
// an apt-style install; local devs install manually — e.g.
// `brew install etcd`).
package etcd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/e6qu/shimanism/internal/nosql/domain"
)

func requireEtcd(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("etcd")
	if err != nil {
		t.Skipf("etcd binary not on PATH (CI installs it via the go-vet+test+build job); skipping live conformance: %v", err)
	}
	return bin
}

// freeTCPPort grabs an ephemeral TCP port the test can hand to etcd.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve TCP addr: %v", err)
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		t.Fatalf("listen TCP: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// startEtcd boots a single-node etcd server on ephemeral ports and
// returns a clientv3 + a cleanup that kills the process.
func startEtcd(t *testing.T) *clientv3.Client {
	t.Helper()
	bin := requireEtcd(t)
	dataDir := t.TempDir()
	clientPort := freeTCPPort(t)
	peerPort := freeTCPPort(t)
	clientURL := fmt.Sprintf("http://127.0.0.1:%d", clientPort)
	peerURL := fmt.Sprintf("http://127.0.0.1:%d", peerPort)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin,
		"--name", "shim-test",
		"--data-dir", filepath.Join(dataDir, "data"),
		"--listen-client-urls", clientURL,
		"--advertise-client-urls", clientURL,
		"--listen-peer-urls", peerURL,
		"--initial-advertise-peer-urls", peerURL,
		"--initial-cluster", "shim-test="+peerURL,
		"--initial-cluster-token", "shim-test",
		"--log-level", "warn",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start etcd: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	// Wait for the client port to accept connections + return a
	// healthy status.
	deadline := time.Now().Add(15 * time.Second)
	var cli *clientv3.Client
	var err error
	for time.Now().Before(deadline) {
		cli, err = clientv3.New(clientv3.Config{
			Endpoints:   []string{clientURL},
			DialTimeout: 500 * time.Millisecond,
		})
		if err == nil {
			pingCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, perr := cli.Status(pingCtx, clientURL)
			cancel()
			if perr == nil {
				t.Cleanup(func() { _ = cli.Close() })
				return cli
			}
			_ = cli.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("etcd never became ready at %s; last err=%v", clientURL, err)
	return nil
}

func TestLive_Etcd_TableLifecycle(t *testing.T) {
	cli := startEtcd(t)
	b := New(cli)
	ctx := context.Background()

	tbl, err := b.CreateTable(ctx, "users", domain.CreateTableOptions{
		PartitionKeyName: "id",
		Description:      "users table",
		Tags:             map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if tbl.PartitionKeyName != "id" {
		t.Errorf("schema mismatch: %+v", tbl)
	}

	// Duplicate.
	if _, err := b.CreateTable(ctx, "users", domain.CreateTableOptions{PartitionKeyName: "id"}); !domain.IsKind(err, domain.KindTableAlreadyExists) {
		t.Errorf("dup CreateTable = %v, want TableAlreadyExists", err)
	}

	// Round-trip.
	got, err := b.GetTable(ctx, "users")
	if err != nil || got.Description != "users table" || got.Tags["env"] != "test" {
		t.Errorf("GetTable round-trip = %+v err=%v", got, err)
	}

	// List.
	list, err := b.ListTables(ctx, domain.ListTablesOptions{})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(list.Tables) != 1 || list.Tables[0].Name != "users" {
		t.Errorf("ListTables = %+v", list.Tables)
	}

	// Reserved names rejected.
	if _, err := b.CreateTable(ctx, "__shim_meta", domain.CreateTableOptions{PartitionKeyName: "id"}); !domain.IsKind(err, domain.KindInvalidArgument) {
		t.Errorf("__-prefixed CreateTable = %v, want InvalidArgument", err)
	}

	// Delete + idempotent re-delete.
	if err := b.DeleteTable(ctx, "users", false); err != nil {
		t.Errorf("DeleteTable: %v", err)
	}
	if _, err := b.GetTable(ctx, "users"); !domain.IsKind(err, domain.KindNoSuchTable) {
		t.Errorf("GetTable after delete = %v, want NoSuchTable", err)
	}
}

func TestLive_Etcd_ItemCRUD_PartitionOnly(t *testing.T) {
	cli := startEtcd(t)
	b := New(cli)
	ctx := context.Background()

	mustCreate(t, b, "kv", domain.CreateTableOptions{PartitionKeyName: "id"})

	// PutItem with every value type.
	item := domain.Item{Attributes: map[string]domain.Value{
		"id":     domain.StringValue("a"),
		"name":   domain.StringValue("alice"),
		"age":    domain.NumberValue("30"),
		"score":  domain.NumberValue("99.5"),
		"big":    domain.NumberValue("9999999999999999999"),
		"vip":    domain.BoolValue(true),
		"avatar": domain.BytesValue([]byte{0xde, 0xad, 0xbe, 0xef}),
		"opt":    domain.NullValue(),
	}}
	if err := b.PutItem(ctx, "kv", item); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	got, err := b.GetItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("a")})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Attributes["name"].Str != "alice" {
		t.Errorf("name = %q", got.Attributes["name"].Str)
	}
	if got.Attributes["age"].Num != "30" {
		t.Errorf("age = %q", got.Attributes["age"].Num)
	}
	if got.Attributes["big"].Num != "9999999999999999999" {
		t.Errorf("big = %q (precision lost)", got.Attributes["big"].Num)
	}
	if !got.Attributes["vip"].Bool {
		t.Errorf("vip = false")
	}
	if string(got.Attributes["avatar"].Bin) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Errorf("avatar bytes round-trip failed: %x", got.Attributes["avatar"].Bin)
	}
	if got.Attributes["opt"].Type != domain.ValueNull {
		t.Errorf("opt type = %s, want null", got.Attributes["opt"].Type)
	}

	// Missing item.
	_, err = b.GetItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("missing")})
	if !domain.IsKind(err, domain.KindNoSuchItem) {
		t.Errorf("GetItem missing = %v, want NoSuchItem", err)
	}

	// Delete + re-delete.
	if err := b.DeleteItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("a")}); err != nil {
		t.Errorf("DeleteItem: %v", err)
	}
	if err := b.DeleteItem(ctx, "kv", domain.Key{PartitionKey: domain.StringValue("a")}); !domain.IsKind(err, domain.KindNoSuchItem) {
		t.Errorf("DeleteItem after delete = %v, want NoSuchItem", err)
	}
}

func TestLive_Etcd_QueryAndScan_CompositeKey(t *testing.T) {
	cli := startEtcd(t)
	b := New(cli)
	ctx := context.Background()

	mustCreate(t, b, "events", domain.CreateTableOptions{
		PartitionKeyName: "user",
		SortKeyName:      "ts",
	})
	puts := []struct {
		user, ts string
	}{
		{"u1", "2026-01-01"},
		{"u1", "2026-01-02"},
		{"u1", "2026-02-01"},
		{"u2", "2026-01-01"},
	}
	for _, p := range puts {
		if err := b.PutItem(ctx, "events", domain.Item{Attributes: map[string]domain.Value{
			"user": domain.StringValue(p.user),
			"ts":   domain.StringValue(p.ts),
		}}); err != nil {
			t.Fatalf("PutItem (%s/%s): %v", p.user, p.ts, err)
		}
	}

	// Query u1 — 3 items.
	q, err := b.Query(ctx, "events", domain.StringValue("u1"), domain.QueryOptions{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(q.Items) != 3 {
		t.Errorf("Query u1 len = %d, want 3", len(q.Items))
	}

	// Query u1 + 2026-01 prefix — 2 items.
	q2, err := b.Query(ctx, "events", domain.StringValue("u1"), domain.QueryOptions{SortKeyPrefix: "2026-01"})
	if err != nil {
		t.Fatalf("Query prefix: %v", err)
	}
	if len(q2.Items) != 2 {
		t.Errorf("Query u1 prefix=2026-01 len = %d, want 2", len(q2.Items))
	}

	// Scan all — 4.
	s, err := b.Scan(ctx, "events", domain.ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Items) != 4 {
		t.Errorf("Scan len = %d, want 4", len(s.Items))
	}

	// Scan with limit + page.
	first, err := b.Scan(ctx, "events", domain.ScanOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Scan limit: %v", err)
	}
	if len(first.Items) != 2 || first.NextPageToken == "" {
		t.Errorf("Scan first page = %+v", first)
	}
	second, err := b.Scan(ctx, "events", domain.ScanOptions{Limit: 2, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatalf("Scan second page: %v", err)
	}
	if len(second.Items) != 2 {
		t.Errorf("Scan second page len = %d, want 2", len(second.Items))
	}
}

func TestLive_Etcd_DeleteTable_ForceVsRefuse(t *testing.T) {
	cli := startEtcd(t)
	b := New(cli)
	ctx := context.Background()

	mustCreate(t, b, "kv", domain.CreateTableOptions{PartitionKeyName: "id"})
	if err := b.PutItem(ctx, "kv", domain.Item{Attributes: map[string]domain.Value{"id": domain.StringValue("a")}}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	if err := b.DeleteTable(ctx, "kv", false); !domain.IsKind(err, domain.KindTableNotEmpty) {
		t.Errorf("DeleteTable force=false on non-empty = %v, want TableNotEmpty", err)
	}
	if err := b.DeleteTable(ctx, "kv", true); err != nil {
		t.Errorf("DeleteTable force=true: %v", err)
	}
}

func TestLive_Etcd_UpdateTableTags(t *testing.T) {
	cli := startEtcd(t)
	b := New(cli)
	ctx := context.Background()

	mustCreate(t, b, "tagged", domain.CreateTableOptions{
		PartitionKeyName: "id",
		Tags:             map[string]string{"env": "test"},
	})
	if err := b.UpdateTableTags(ctx, "tagged", map[string]string{"team": "data"}); err != nil {
		t.Fatalf("UpdateTableTags: %v", err)
	}
	got, _ := b.GetTable(ctx, "tagged")
	if got.Tags["team"] != "data" || got.Tags["env"] != "" {
		t.Errorf("Tags after Update = %v, want only team=data", got.Tags)
	}
	if err := b.UpdateTableTags(ctx, "missing", map[string]string{"x": "y"}); !domain.IsKind(err, domain.KindNoSuchTable) {
		t.Errorf("UpdateTableTags missing = %v, want NoSuchTable", err)
	}
}

// ---- helpers ----

func mustCreate(t *testing.T, b *Backend, name string, opt domain.CreateTableOptions) {
	t.Helper()
	if _, err := b.CreateTable(context.Background(), name, opt); err != nil {
		t.Fatalf("CreateTable %s: %v", name, err)
	}
}

// keep imports referenced even in skip paths.
var _ = strconv.Itoa
var _ = os.Args
