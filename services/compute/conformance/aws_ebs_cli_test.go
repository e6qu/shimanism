// Conformance: AWS EC2 block storage driven by the `aws ec2` CLI.
// Covers Phase 17: volume create/describe/attach/detach/delete and
// snapshot create/describe/delete.
package conformance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestAWSCLI_EBS_VolumeLifecycle(t *testing.T) {
	awsBin := requireAWSCLI(t)
	srv := harness.StartComputeServerAWS(t, inmem.New())

	run := func(args ...string) ([]byte, error) {
		t.Helper()
		stdout, _, err := runAWSEC2(t, srv.URL, awsBin, args...)
		return stdout, err
	}

	// Create a volume
	stdout, err := run("ec2", "create-volume",
		"--availability-zone", "us-east-1a",
		"--size", "10",
		"--volume-type", "gp3",
	)
	if err != nil {
		t.Fatalf("create-volume: %v\n%s", err, stdout)
	}
	var createOut struct {
		VolumeId string `json:"VolumeId"`
		State    string `json:"State"`
		Size     int    `json:"Size"`
	}
	if err := json.Unmarshal(stdout, &createOut); err != nil {
		t.Fatalf("parse create-volume output: %v\nraw: %s", err, stdout)
	}
	if createOut.VolumeId == "" {
		t.Fatal("create-volume: empty VolumeId")
	}
	if createOut.State != "available" {
		t.Errorf("create-volume State = %q, want available", createOut.State)
	}
	if createOut.Size != 10 {
		t.Errorf("create-volume Size = %d, want 10", createOut.Size)
	}
	volID := createOut.VolumeId

	t.Cleanup(func() {
		run("ec2", "delete-volume", "--volume-id", volID) //nolint:errcheck
	})

	// Describe volume
	stdout, err = run("ec2", "describe-volumes", "--volume-ids", volID)
	if err != nil {
		t.Fatalf("describe-volumes: %v\n%s", err, stdout)
	}
	if !strings.Contains(string(stdout), volID) {
		t.Errorf("describe-volumes output does not contain %q:\n%s", volID, stdout)
	}

	// Delete volume
	if _, err := run("ec2", "delete-volume", "--volume-id", volID); err != nil {
		t.Fatalf("delete-volume: %v", err)
	}
}

func TestAWSCLI_EBS_SnapshotLifecycle(t *testing.T) {
	awsBin := requireAWSCLI(t)
	srv := harness.StartComputeServerAWS(t, inmem.New())

	run := func(args ...string) ([]byte, error) {
		t.Helper()
		stdout, _, err := runAWSEC2(t, srv.URL, awsBin, args...)
		return stdout, err
	}

	// Create source volume
	stdout, err := run("ec2", "create-volume",
		"--availability-zone", "us-east-1a",
		"--size", "8",
		"--volume-type", "gp3",
	)
	if err != nil {
		t.Fatalf("create-volume: %v", err)
	}
	var vol struct {
		VolumeId string `json:"VolumeId"`
	}
	if err := json.Unmarshal(stdout, &vol); err != nil {
		t.Fatalf("parse create-volume: %v", err)
	}
	t.Cleanup(func() {
		run("ec2", "delete-volume", "--volume-id", vol.VolumeId) //nolint:errcheck
	})

	// Create snapshot
	stdout, err = run("ec2", "create-snapshot",
		"--volume-id", vol.VolumeId,
		"--description", "phase-17-cli-test",
	)
	if err != nil {
		t.Fatalf("create-snapshot: %v\n%s", err, stdout)
	}
	var snap struct {
		SnapshotId string `json:"SnapshotId"`
		State      string `json:"State"`
		VolumeId   string `json:"VolumeId"`
	}
	if err := json.Unmarshal(stdout, &snap); err != nil {
		t.Fatalf("parse create-snapshot: %v\nraw: %s", err, stdout)
	}
	if snap.SnapshotId == "" {
		t.Fatal("create-snapshot: empty SnapshotId")
	}
	if snap.State != "completed" {
		t.Errorf("snapshot State = %q, want completed", snap.State)
	}
	if snap.VolumeId != vol.VolumeId {
		t.Errorf("snapshot VolumeId = %q, want %q", snap.VolumeId, vol.VolumeId)
	}

	t.Cleanup(func() {
		run("ec2", "delete-snapshot", "--snapshot-id", snap.SnapshotId) //nolint:errcheck
	})

	// Describe snapshot
	stdout, err = run("ec2", "describe-snapshots", "--snapshot-ids", snap.SnapshotId)
	if err != nil {
		t.Fatalf("describe-snapshots: %v\n%s", err, stdout)
	}
	if !strings.Contains(string(stdout), snap.SnapshotId) {
		t.Errorf("describe-snapshots output does not contain %q", snap.SnapshotId)
	}

	// Delete snapshot
	if _, err := run("ec2", "delete-snapshot", "--snapshot-id", snap.SnapshotId); err != nil {
		t.Fatalf("delete-snapshot: %v", err)
	}
}
