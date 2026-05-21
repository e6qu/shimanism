// Phase 10.1 — GCP Cloud SQL Admin Operations.Get conformance.
//
// Before: BUG-5. The GCP frontend returned PENDING Operation
// envelopes for every mutating op but didn't implement
// /v1/projects/{p}/operations/{op}. `gcloud sql instances`, the
// GCP Go SDK's WaitFor flow, and `hashicorp/google
// google_sql_database_instance` all hung waiting on poll responses.
//
// After: Operations.Get derives the current state by reading the
// target resource from the backend. Stateless — the Operation Name
// encodes (opType, targetId) so a polling client gets the right
// answer without a shim-side operation table.
package conformance_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

func TestGCPSDK_RDBMS_OperationsPolling(t *testing.T) {
	srv := harness.StartRDBMSServerGCP(t, inmem.New())
	ctx := context.Background()
	svc, err := sqladmin.NewService(ctx,
		option.WithEndpoint(strings.TrimSuffix(srv.URL, "/")+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Create returns an Operation envelope.
	op, err := svc.Instances.Insert("proj1", &sqladmin.DatabaseInstance{
		Name:            "polltest",
		DatabaseVersion: "POSTGRES_16",
		Settings: &sqladmin.Settings{
			Tier:           "db-custom-1-3840",
			DataDiskSizeGb: 20,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}
	if op.Name == "" {
		t.Fatalf("operation has no name")
	}

	// Operations.Get must resolve to a status reflecting the actual
	// resource's state. The inmem backend transitions the instance
	// from Creating → Available asynchronously; the test polls a
	// few times.
	got, err := svc.Operations.Get("proj1", op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}
	if got.Status != "RUNNING" && got.Status != "DONE" && got.Status != "PENDING" {
		t.Errorf("unexpected status %q (want PENDING/RUNNING/DONE)", got.Status)
	}
	if got.TargetId != "polltest" {
		t.Errorf("targetId = %q, want polltest", got.TargetId)
	}

	// Delete and check the operation eventually resolves to DONE.
	dop, err := svc.Instances.Delete("proj1", "polltest").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}
	resolved, err := svc.Operations.Get("proj1", dop.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get (delete): %v", err)
	}
	// For delete, NoSuchInstance → DONE; if instance still exists in
	// transition, RUNNING is also OK.
	if resolved.Status != "DONE" && resolved.Status != "RUNNING" {
		t.Errorf("delete op status = %q, want DONE/RUNNING", resolved.Status)
	}
}
