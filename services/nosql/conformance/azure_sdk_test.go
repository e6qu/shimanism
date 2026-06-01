// Conformance: Azure Cosmos DB Table API-shaped frontend exercised
// by the official aztables SDK. SharedKey credentials match what
// the shim's verifier trusts; the SDK is pointed at the shim
// through the standard endpoint-override path.
package conformance_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

const (
	azAccount = "shimcosmos"
	// The harness's SharedKey middleware accepts this key (matches
	// internal/harness/server.go's StartNoSQLServerAzure).
	azKey = "dGVzdC1rZXktZG8tbm90LXVzZS1pbi1wcm9kLXRoaXMtaXMtMzItYnl0ZXMtb2YtanVuaw=="
)

func newCosmosTablesClient(t *testing.T, endpoint string) *aztables.ServiceClient {
	t.Helper()
	cred, err := aztables.NewSharedKeyCredential(azAccount, azKey)
	if err != nil {
		t.Fatalf("SharedKeyCredential: %v", err)
	}
	// aztables uses the endpoint URL as-is; pass the harness URL.
	svc, err := aztables.NewServiceClientWithSharedKey(endpoint, cred, nil)
	if err != nil {
		t.Fatalf("NewServiceClientWithSharedKey: %v", err)
	}
	return svc
}

func TestAzureSDK_CosmosTables_TableLifecycle(t *testing.T) {
	srv := harness.StartNoSQLServerAzure(t, inmem.New())
	svc := newCosmosTablesClient(t, srv.URL)
	ctx := context.Background()

	if _, err := svc.CreateTable(ctx, "users", nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Duplicate CreateTable fails with 409 / TableAlreadyExists.
	_, err := svc.CreateTable(ctx, "users", nil)
	if err == nil {
		t.Fatal("duplicate CreateTable: want error, got nil")
	}
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 409 {
		t.Errorf("duplicate CreateTable error = %v (status %d), want 409", err, respErr.StatusCode)
	}

	// List sees it.
	pager := svc.NewListTablesPager(nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListTables: %v", err)
		}
		for _, tbl := range page.Tables {
			if tbl.Name != nil && *tbl.Name == "users" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("ListTables did not include 'users'")
	}

	if _, err := svc.DeleteTable(ctx, "users", nil); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
}

func TestAzureSDK_CosmosTables_EntityCRUD(t *testing.T) {
	srv := harness.StartNoSQLServerAzure(t, inmem.New())
	svc := newCosmosTablesClient(t, srv.URL)
	ctx := context.Background()

	if _, err := svc.CreateTable(ctx, "kv", nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	client := svc.NewClient("kv")

	// Insert.
	entity := map[string]any{
		"PartitionKey": "p1",
		"RowKey":       "r1",
		"Name":         "Alice",
		"Age":          int64(30),
		"Active":       true,
	}
	body, _ := json.Marshal(entity)
	if _, err := client.AddEntity(ctx, body, nil); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// Get.
	got, err := client.GetEntity(ctx, "p1", "r1", nil)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(got.Value, &raw); err != nil {
		t.Fatalf("decode entity: %v\n%s", err, got.Value)
	}
	if raw["Name"] != "Alice" {
		t.Errorf("Name = %v, want Alice", raw["Name"])
	}
	if raw["Active"] != true {
		t.Errorf("Active = %v, want true", raw["Active"])
	}

	// List.
	pager := client.NewListEntitiesPager(nil)
	count := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListEntities: %v", err)
		}
		count += len(page.Entities)
	}
	if count != 1 {
		t.Errorf("ListEntities count = %d, want 1", count)
	}

	// Delete.
	if _, err := client.DeleteEntity(ctx, "p1", "r1", nil); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
	if _, err := client.GetEntity(ctx, "p1", "r1", nil); err == nil {
		t.Error("GetEntity after delete: want error, got nil")
	}
}

func TestAzureSDK_CosmosTables_QueryByPartition(t *testing.T) {
	srv := harness.StartNoSQLServerAzure(t, inmem.New())
	svc := newCosmosTablesClient(t, srv.URL)
	ctx := context.Background()

	if _, err := svc.CreateTable(ctx, "events", nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	client := svc.NewClient("events")

	puts := []struct {
		pk, rk string
	}{
		{"u1", "2026-01-01"},
		{"u1", "2026-01-02"},
		{"u2", "2026-01-01"},
	}
	for _, p := range puts {
		body, _ := json.Marshal(map[string]any{
			"PartitionKey": p.pk,
			"RowKey":       p.rk,
		})
		if _, err := client.AddEntity(ctx, body, nil); err != nil {
			t.Fatalf("AddEntity (%s/%s): %v", p.pk, p.rk, err)
		}
	}

	filter := "PartitionKey eq 'u1'"
	pager := client.NewListEntitiesPager(&aztables.ListEntitiesOptions{Filter: &filter})
	count := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		count += len(page.Entities)
	}
	if count != 2 {
		t.Errorf("Query u1 count = %d, want 2", count)
	}
}
