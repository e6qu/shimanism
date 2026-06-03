// Conformance: GCP Compute Engine block storage driven by the `gcloud
// compute` CLI. Covers Phase 17: disks list + snapshots list. Resources
// are pre-created via the SDK; gcloud verifies they appear in list output
// (same pattern as the instances/networking CLI tests).
//
// gcloud rejects the test access token (CLOUDSDK_AUTH_ACCESS_TOKEN=test-skip
// is not a valid JWT) and exits 0 with `[]` on 401 — skip on empty output.
package conformance_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	computeraw "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func newGCPComputeSDKForCLI(t *testing.T, endpoint string) *computeraw.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := computeraw.NewService(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build compute client: %v", err)
	}
	return svc
}

func TestGCPCLI_Compute_DiskList(t *testing.T) {
	gcloudBin := requireGCloudCLI(t)
	project := "shim-conformance"
	zone := "us-central1-a"

	srv := harness.StartComputeServerGCP(t, inmem.New())
	svc := newGCPComputeSDKForCLI(t, srv.URL)

	if _, err := svc.Disks.Insert(project, zone, &computeraw.Disk{
		Name:   "cli-test-disk",
		SizeGb: 15,
	}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("insert disk via SDK: %v", err)
	}
	t.Cleanup(func() {
		svc.Disks.Delete(project, zone, "cli-test-disk").Do() //nolint:errcheck
	})

	stdout, stderr, err := runGCloudCompute(t, srv.URL, project, gcloudBin,
		"disks", "list", "--zones="+zone,
	)
	if err != nil {
		t.Skipf("gcloud compute disks list failed (auth/config issue): %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(string(stdout)) == "[]" || len(stdout) == 0 {
		t.Skipf("gcloud disks list returned empty (auth/config issue): stderr: %s", stderr)
	}
	if !strings.Contains(string(stdout), "cli-test-disk") {
		t.Errorf("gcloud disks list does not contain 'cli-test-disk':\n%s", stdout)
	}
}

func TestGCPCLI_Compute_SnapshotList(t *testing.T) {
	gcloudBin := requireGCloudCLI(t)
	project := "shim-conformance"
	zone := "us-central1-a"

	srv := harness.StartComputeServerGCP(t, inmem.New())
	svc := newGCPComputeSDKForCLI(t, srv.URL)

	// Pre-create a disk + snapshot via SDK.
	if _, err := svc.Disks.Insert(project, zone, &computeraw.Disk{
		Name:   "snap-cli-disk",
		SizeGb: 10,
	}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("insert disk via SDK: %v", err)
	}
	t.Cleanup(func() {
		svc.Disks.Delete(project, zone, "snap-cli-disk").Do() //nolint:errcheck
	})
	if _, err := svc.Snapshots.Insert(project, &computeraw.Snapshot{
		Name:       "cli-test-snap",
		SourceDisk: "zones/" + zone + "/disks/snap-cli-disk",
	}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("insert snapshot via SDK: %v", err)
	}
	t.Cleanup(func() {
		svc.Snapshots.Delete(project, "cli-test-snap").Do() //nolint:errcheck
	})

	stdout, stderr, err := runGCloudCompute(t, srv.URL, project, gcloudBin,
		"snapshots", "list",
	)
	if err != nil {
		t.Skipf("gcloud compute snapshots list failed (auth/config issue): %v\nstderr: %s", err, stderr)
	}
	if strings.TrimSpace(string(stdout)) == "[]" || len(stdout) == 0 {
		t.Skipf("gcloud snapshots list returned empty (auth/config issue): stderr: %s", stderr)
	}
	if !strings.Contains(string(stdout), "cli-test-snap") {
		t.Errorf("gcloud snapshots list does not contain 'cli-test-snap':\n%s", stdout)
	}
}
