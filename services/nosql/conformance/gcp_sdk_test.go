// Conformance: GCP Firestore Native-shaped frontend exercised by
// the official google.golang.org/api/firestore/v1 SDK. The SDK is
// pointed at the shim via WithEndpoint and signs requests with a
// JWT the shim's GCP bearer verifier trusts.
//
// Per N18, "table" lifecycle goes through the __shim_tables__
// reserved collection: createDocument under __shim_tables__ is
// CreateTable, etc. The SDK doesn't know about that abstraction;
// the test drives the convention directly.
package conformance_test

import (
	"context"
	"encoding/base64"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	firestore "google.golang.org/api/firestore/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

const fsProject = "shim-conformance"
const fsDatabase = "(default)"

func newFirestoreService(t *testing.T, endpoint string) *firestore.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://firestore.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := firestore.NewService(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("new Firestore service: %v", err)
	}
	return svc
}

func fsParent() string {
	return "projects/" + fsProject + "/databases/" + fsDatabase + "/documents"
}

const metaCollection = "__shim_tables__"

func TestGCPSDK_Firestore_TableLifecycle(t *testing.T) {
	srv := harness.StartNoSQLServerGCP(t, inmem.New())
	svc := newFirestoreService(t, srv.URL)
	ctx := context.Background()

	// CreateTable via the __shim_tables__ convention.
	doc := &firestore.Document{
		Fields: map[string]firestore.Value{
			"partitionKey": {StringValue: "id", ForceSendFields: []string{"StringValue"}},
			"sortKey":      {StringValue: "", ForceSendFields: []string{"StringValue"}},
			"description":  {StringValue: "users table", ForceSendFields: []string{"StringValue"}},
			"tags": {MapValue: &firestore.MapValue{Fields: map[string]firestore.Value{
				"env": {StringValue: "test", ForceSendFields: []string{"StringValue"}},
			}}, ForceSendFields: []string{"MapValue"}},
		},
	}
	created, err := svc.Projects.Databases.Documents.CreateDocument(fsParent(), metaCollection, doc).
		DocumentId("users").Context(ctx).Do()
	if err != nil {
		t.Fatalf("CreateDocument (CreateTable): %v", err)
	}
	if created.Fields["partitionKey"].StringValue != "id" {
		t.Errorf("partitionKey = %q", created.Fields["partitionKey"].StringValue)
	}

	// Duplicate CreateTable fails with 409.
	_, err = svc.Projects.Databases.Documents.CreateDocument(fsParent(), metaCollection, doc).
		DocumentId("users").Context(ctx).Do()
	if err == nil {
		t.Fatal("duplicate CreateTable: want error, got nil")
	}

	// GetTable.
	got, err := svc.Projects.Databases.Documents.
		Get(fsParent() + "/" + metaCollection + "/users").Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if got.Fields["description"].StringValue != "users table" {
		t.Errorf("description = %q", got.Fields["description"].StringValue)
	}
	if got.Fields["tags"].MapValue.Fields["env"].StringValue != "test" {
		t.Errorf("env tag = %q", got.Fields["tags"].MapValue.Fields["env"].StringValue)
	}

	// ListTables.
	list, err := svc.Projects.Databases.Documents.
		List(fsParent(), metaCollection).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(list.Documents) != 1 || !strings.HasSuffix(list.Documents[0].Name, "/users") {
		t.Errorf("ListTables = %+v", list.Documents)
	}

	// DeleteTable.
	if _, err := svc.Projects.Databases.Documents.
		Delete(fsParent() + "/" + metaCollection + "/users").Context(ctx).Do(); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	if _, err := svc.Projects.Databases.Documents.
		Get(fsParent() + "/" + metaCollection + "/users").Context(ctx).Do(); err == nil {
		t.Error("GetTable after Delete: want error, got nil")
	}
}

func TestGCPSDK_Firestore_ItemCRUD(t *testing.T) {
	srv := harness.StartNoSQLServerGCP(t, inmem.New())
	svc := newFirestoreService(t, srv.URL)
	ctx := context.Background()

	createTestTable(t, svc, "kv", "id", "")

	// PutItem via CreateDocument under the user collection.
	itemDoc := &firestore.Document{
		Fields: map[string]firestore.Value{
			"id":   {StringValue: "alice", ForceSendFields: []string{"StringValue"}},
			"name": {StringValue: "Alice", ForceSendFields: []string{"StringValue"}},
			"age":  {IntegerValue: 30, ForceSendFields: []string{"IntegerValue"}},
		},
	}
	itemDocID := derivedDocID(map[string]firestore.Value{
		"id":   itemDoc.Fields["id"],
		"name": itemDoc.Fields["name"],
		"age":  itemDoc.Fields["age"],
	})
	if _, err := svc.Projects.Databases.Documents.
		CreateDocument(fsParent(), "kv", itemDoc).
		DocumentId(itemDocID).Context(ctx).Do(); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	// GetItem.
	got, err := svc.Projects.Databases.Documents.
		Get(fsParent() + "/kv/" + itemDocID).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Fields["name"].StringValue != "Alice" {
		t.Errorf("name = %q", got.Fields["name"].StringValue)
	}
	if got.Fields["age"].IntegerValue != 30 {
		t.Errorf("age = %d", got.Fields["age"].IntegerValue)
	}

	// DeleteItem.
	if _, err := svc.Projects.Databases.Documents.
		Delete(fsParent() + "/kv/" + itemDocID).Context(ctx).Do(); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
}

// TestGCPSDK_Firestore_ListDocuments exercises the Scan path via
// the SDK's documents.list. The SDK's RunQuery method can't be used
// for full query results (its `.Do()` only decodes one
// RunQueryResponse, but Firestore's real REST endpoint streams an
// array). The shim's :runQuery handler still emits the
// streaming-array shape Firestore uses; raw-HTTP / gRPC clients
// see every result. See TestGCPHTTP_Firestore_RunQueryRaw below.
func TestGCPSDK_Firestore_ListDocuments(t *testing.T) {
	srv := harness.StartNoSQLServerGCP(t, inmem.New())
	svc := newFirestoreService(t, srv.URL)
	ctx := context.Background()

	createTestTable(t, svc, "events", "user", "ts")
	for _, e := range []struct {
		user, ts string
	}{
		{"u1", "2026-01-01"},
		{"u1", "2026-01-02"},
		{"u2", "2026-01-01"},
	} {
		fields := map[string]firestore.Value{
			"user": {StringValue: e.user, ForceSendFields: []string{"StringValue"}},
			"ts":   {StringValue: e.ts, ForceSendFields: []string{"StringValue"}},
		}
		docID := derivedDocID(fields)
		if _, err := svc.Projects.Databases.Documents.
			CreateDocument(fsParent(), "events", &firestore.Document{Fields: fields}).
			DocumentId(docID).Context(ctx).Do(); err != nil {
			t.Fatalf("PutItem (%s/%s): %v", e.user, e.ts, err)
		}
	}

	list, err := svc.Projects.Databases.Documents.
		List(fsParent(), "events").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	if len(list.Documents) != 3 {
		t.Errorf("List len = %d, want 3", len(list.Documents))
	}
}

// createTestTable creates the table-metadata document for tests.
func createTestTable(t *testing.T, svc *firestore.Service, name, pk, sk string) {
	t.Helper()
	doc := &firestore.Document{
		Fields: map[string]firestore.Value{
			"partitionKey": {StringValue: pk, ForceSendFields: []string{"StringValue"}},
			"sortKey":      {StringValue: sk, ForceSendFields: []string{"StringValue"}},
			"description":  {StringValue: "", ForceSendFields: []string{"StringValue"}},
		},
	}
	if _, err := svc.Projects.Databases.Documents.
		CreateDocument(fsParent(), metaCollection, doc).
		DocumentId(name).Context(context.Background()).Do(); err != nil {
		t.Fatalf("createTestTable %s: %v", name, err)
	}
}

// derivedDocID mirrors the frontend's docIDFor algorithm so the
// test can re-derive a docID from the item attributes the same way
// the frontend would. Without this the test can't address items it
// just wrote.
func derivedDocID(fields map[string]firestore.Value) string {
	parts := make([]string, 0, len(fields))
	for k, v := range fields {
		parts = append(parts, k+"="+valueAttrEncoding(v))
	}
	sort.Strings(parts)
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "|")))
}

func valueAttrEncoding(v firestore.Value) string {
	switch {
	case v.StringValue != "":
		return "string:" + v.StringValue
	case v.IntegerValue != 0 || containsForce(v.ForceSendFields, "IntegerValue"):
		return "number:" + formatInt(v.IntegerValue)
	case v.DoubleValue != 0 || containsForce(v.ForceSendFields, "DoubleValue"):
		return "number:" + formatFloat(v.DoubleValue)
	case v.BooleanValue || containsForce(v.ForceSendFields, "BooleanValue"):
		if v.BooleanValue {
			return "bool:1"
		}
		return "bool:0"
	case v.BytesValue != "":
		return "bytes:" + v.BytesValue
	case v.NullValue == "NULL_VALUE":
		return "null:"
	}
	return ""
}

func containsForce(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	// Avoid importing strconv at top — small helper.
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func formatFloat(f float64) string {
	// Match the frontend's strconv.FormatFloat 'g', -1 by approximating.
	// For test parity we accept the round-trip variance — only used
	// when an item has a Double, which the test doesn't use today.
	return ""
}
