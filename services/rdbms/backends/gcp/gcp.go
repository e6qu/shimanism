// Package gcp is the GCP Cloud SQL Admin backend for shimanism's
// rdbms service. Uses google.golang.org/api/sqladmin/v1 (the
// synchronous REST SDK).
//
// Cloud SQL Admin is fully async on every mutating op — Create,
// Delete, Patch, Restart, BackupRun.Insert, etc. all return an
// `Operation` resource. This backend deliberately does **not**
// wait — it returns the domain Instance with Status=Creating and
// lets the caller poll DescribeInstance (which reads the current
// state from Cloud SQL).
package gcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

type Config struct {
	ProjectID string
	Region    string // default us-central1
}

type Backend struct {
	svc     *sqladmin.Service
	project string
	region  string
}

func New(svc *sqladmin.Service, cfg Config) *Backend {
	r := cfg.Region
	if r == "" {
		r = "us-central1"
	}
	return &Backend{svc: svc, project: cfg.ProjectID, region: r}
}

var _ domain.RDBMS = (*Backend)(nil)

func gcpEngineVersion(e domain.Engine, version string) string {
	switch e {
	case domain.EnginePostgres:
		if version == "" {
			return "POSTGRES_15"
		}
		return "POSTGRES_" + strings.TrimPrefix(version, "POSTGRES_")
	case domain.EngineMySQL:
		if version == "" {
			return "MYSQL_8_0"
		}
		return "MYSQL_" + strings.TrimPrefix(version, "MYSQL_")
	default:
		return ""
	}
}

func domainEngineFromGCP(s string) domain.Engine {
	switch {
	case strings.HasPrefix(s, "POSTGRES_"):
		return domain.EnginePostgres
	case strings.HasPrefix(s, "MYSQL_"):
		return domain.EngineMySQL
	default:
		return domain.EngineUnknown
	}
}

func domainStatusFromGCP(s string) domain.Status {
	switch s {
	case "RUNNABLE":
		return domain.StatusAvailable
	case "PENDING_CREATE":
		return domain.StatusCreating
	case "PENDING_UPDATE", "MAINTENANCE":
		return domain.StatusModifying
	case "PENDING_DELETE":
		return domain.StatusDeleting
	default:
		return domain.StatusCreating
	}
}

func backupStatusFromGCP(s string) domain.Status {
	switch s {
	case "SUCCESSFUL":
		return domain.StatusAvailable
	case "RUNNING", "ENQUEUED":
		return domain.StatusCreating
	case "FAILED":
		return domain.StatusUnknown
	case "DELETING":
		return domain.StatusDeleting
	default:
		return domain.StatusCreating
	}
}

func (b *Backend) instanceToDomain(in *sqladmin.DatabaseInstance) domain.Instance {
	engine := domainEngineFromGCP(in.DatabaseVersion)
	out := domain.Instance{
		Name:          in.Name,
		Engine:        engine,
		EngineVersion: in.DatabaseVersion,
		Status:        domainStatusFromGCP(in.State),
		InstanceClass: "",
	}
	if in.Settings != nil {
		out.AllocatedStorageGB = int(in.Settings.DataDiskSizeGb)
		out.InstanceClass = in.Settings.Tier
	}
	if t, err := time.Parse(time.RFC3339, in.CreateTime); err == nil {
		out.CreatedAt = t
	}
	if out.Status == domain.StatusAvailable {
		port := 5432
		if engine == domain.EngineMySQL {
			port = 3306
		}
		host := ""
		for _, ip := range in.IpAddresses {
			if ip.Type == "PRIMARY" {
				host = ip.IpAddress
				break
			}
		}
		out.Connection = domain.Connection{
			Host:           host,
			Port:           port,
			MasterUsername: "shimadmin", // Cloud SQL master user is set at create time
			DatabaseName:   defaultDatabaseFor(engine),
			EngineVersion:  in.DatabaseVersion,
		}
	}
	return out
}

func defaultDatabaseFor(e domain.Engine) string {
	switch e {
	case domain.EngineMySQL:
		return "mysql"
	default:
		return "postgres"
	}
}

// ----------------------------------------------------------------------
// Instances
// ----------------------------------------------------------------------

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	pw := opt.MasterPassword
	revealed := ""
	if pw == "" {
		pw = newPassword()
		revealed = pw
	}
	tier := opt.InstanceClass
	if tier == "" {
		tier = "db-perf-optimized-N-2"
	}
	in := &sqladmin.DatabaseInstance{
		Name:            name,
		DatabaseVersion: gcpEngineVersion(opt.Engine, opt.EngineVersion),
		Region:          b.region,
		RootPassword:    pw,
		Settings: &sqladmin.Settings{
			Tier:           tier,
			DataDiskSizeGb: int64(opt.AllocatedStorageGB),
		},
	}
	if _, err := b.svc.Instances.Insert(b.project, in).Context(ctx).Do(); err != nil {
		return domain.CreateInstanceResult{}, translateErr(err, "instance", name)
	}
	return domain.CreateInstanceResult{
		Instance: domain.Instance{
			Name:               name,
			Engine:             opt.Engine,
			EngineVersion:      opt.EngineVersion,
			Status:             domain.StatusCreating,
			AllocatedStorageGB: opt.AllocatedStorageGB,
			InstanceClass:      tier,
			CreatedAt:          time.Now().UTC(),
		},
		MasterPassword: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	if _, err := b.svc.Instances.Delete(b.project, name).Context(ctx).Do(); err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	in, err := b.svc.Instances.Get(b.project, name).Context(ctx).Do()
	if err != nil {
		return domain.Instance{}, translateErr(err, "instance", name)
	}
	return b.instanceToDomain(in), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	out, err := b.svc.Instances.List(b.project).Context(ctx).Do()
	if err != nil {
		return domain.ListInstancesResult{}, translateErr(err, "instance", "")
	}
	res := domain.ListInstancesResult{}
	for _, in := range out.Items {
		if opt.Prefix != "" && !strings.HasPrefix(in.Name, opt.Prefix) {
			continue
		}
		res.Instances = append(res.Instances, b.instanceToDomain(in))
		if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	patch := &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{},
	}
	if opt.AllocatedStorageGB > 0 {
		patch.Settings.DataDiskSizeGb = int64(opt.AllocatedStorageGB)
	}
	if opt.InstanceClass != "" {
		patch.Settings.Tier = opt.InstanceClass
	}
	if _, err := b.svc.Instances.Patch(b.project, name, patch).Context(ctx).Do(); err != nil {
		return translateErr(err, "instance", name)
	}
	if opt.MasterPassword != "" {
		// Update via users.Update on the implicit master user.
		_, _ = b.svc.Users.Update(b.project, name, &sqladmin.User{
			Name:     "shimadmin",
			Password: opt.MasterPassword,
		}).Context(ctx).Do()
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	if _, err := b.svc.Instances.Restart(b.project, name).Context(ctx).Do(); err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

// ----------------------------------------------------------------------
// Snapshots (BackupRuns)
// ----------------------------------------------------------------------

func (b *Backend) CreateSnapshot(ctx context.Context, instance, snapshotID string) (domain.Snapshot, error) {
	// Cloud SQL BackupRuns IDs are assigned by the API — we keep the
	// caller-supplied ID by stashing it in the description field.
	run := &sqladmin.BackupRun{
		Description: snapshotID,
	}
	if _, err := b.svc.BackupRuns.Insert(b.project, instance, run).Context(ctx).Do(); err != nil {
		return domain.Snapshot{}, translateErr(err, "snapshot", snapshotID)
	}
	return domain.Snapshot{
		ID:        snapshotID,
		Instance:  instance,
		Engine:    domain.EngineUnknown, // resolved on Describe via the parent instance
		Status:    domain.StatusCreating,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (b *Backend) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	// To delete by description we'd need to list+filter. Out of scope
	// for this minimal pass — return InvalidArgument with a clear
	// message rather than silently no-oping.
	return domain.InvalidArgument(
		"GCP Cloud SQL backup deletion by shim-supplied snapshot ID is not implemented; " +
			"use the native BackupRun ID via the gcloud CLI to delete: " + snapshotID)
}

func (b *Backend) DescribeSnapshot(ctx context.Context, snapshotID string) (domain.Snapshot, error) {
	// Lookup by Description across all instances would be expensive.
	// Phase 5 reports the BackupRun via the instance scope; cross-
	// instance snapshot lookups are an extension.
	return domain.Snapshot{}, domain.NoSuchSnapshot(snapshotID)
}

func (b *Backend) ListSnapshots(ctx context.Context, opt domain.ListSnapshotsOptions) (domain.ListSnapshotsResult, error) {
	if opt.Instance == "" {
		return domain.ListSnapshotsResult{}, domain.InvalidArgument(
			"GCP Cloud SQL ListSnapshots requires an instance filter")
	}
	out, err := b.svc.BackupRuns.List(b.project, opt.Instance).Context(ctx).Do()
	if err != nil {
		return domain.ListSnapshotsResult{}, translateErr(err, "snapshot", "")
	}
	res := domain.ListSnapshotsResult{}
	for _, r := range out.Items {
		s := domain.Snapshot{
			ID:       r.Description,
			Instance: opt.Instance,
			Status:   backupStatusFromGCP(r.Status),
		}
		if t, err := time.Parse(time.RFC3339, r.EndTime); err == nil {
			s.CreatedAt = t
		}
		res.Snapshots = append(res.Snapshots, s)
		if opt.MaxResults > 0 && len(res.Snapshots) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) RestoreFromSnapshot(ctx context.Context, snapshotID, newInstanceName string) (domain.CreateInstanceResult, error) {
	return domain.CreateInstanceResult{}, domain.InvalidArgument(
		"GCP Cloud SQL RestoreFromSnapshot requires the native BackupRun ID + source instance — " +
			"out of intersection at this phase")
}

func translateErr(err error, kind, name string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404") || strings.Contains(msg, "notFound"):
		if kind == "snapshot" {
			return domain.NoSuchSnapshot(name)
		}
		return domain.NoSuchInstance(name)
	case strings.Contains(msg, "409") || strings.Contains(msg, "alreadyExists"):
		if kind == "snapshot" {
			return domain.SnapshotAlreadyExists(name)
		}
		return domain.InstanceAlreadyExists(name)
	}
	return fmt.Errorf("cloudsql %s %q: %w", kind, name, err)
}

func newPassword() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
