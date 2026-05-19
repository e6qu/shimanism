// Package aws is the AWS RDS passthrough backend for shimanism's
// rdbms service. Direct mapping over `aws-sdk-go-v2/service/rds`.
package aws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

type Backend struct {
	c *rds.Client
}

func New(c *rds.Client) *Backend { return &Backend{c: c} }

var _ domain.RDBMS = (*Backend)(nil)

func awsEngine(e domain.Engine) string {
	switch e {
	case domain.EnginePostgres:
		return "postgres"
	case domain.EngineMySQL:
		return "mysql"
	default:
		return ""
	}
}

func domainEngine(s string) domain.Engine {
	switch s {
	case "postgres", "aurora-postgresql":
		return domain.EnginePostgres
	case "mysql", "aurora-mysql":
		return domain.EngineMySQL
	default:
		return domain.EngineUnknown
	}
}

func domainStatus(s string) domain.Status {
	switch s {
	case "creating":
		return domain.StatusCreating
	case "available":
		return domain.StatusAvailable
	case "modifying", "backing-up":
		return domain.StatusModifying
	case "rebooting":
		return domain.StatusRebooting
	case "deleting":
		return domain.StatusDeleting
	default:
		return domain.StatusUnknown
	}
}

func snapshotStatus(s string) domain.Status {
	switch s {
	case "creating":
		return domain.StatusCreating
	case "available":
		return domain.StatusAvailable
	case "deleting":
		return domain.StatusDeleting
	default:
		return domain.StatusUnknown
	}
}

func (b *Backend) instanceToDomain(in *rdstypes.DBInstance) domain.Instance {
	out := domain.Instance{
		Name:               awsapi.ToString(in.DBInstanceIdentifier),
		Engine:             domainEngine(awsapi.ToString(in.Engine)),
		EngineVersion:      awsapi.ToString(in.EngineVersion),
		Status:             domainStatus(awsapi.ToString(in.DBInstanceStatus)),
		AllocatedStorageGB: int(awsapi.ToInt32(in.AllocatedStorage)),
		InstanceClass:      awsapi.ToString(in.DBInstanceClass),
	}
	if in.InstanceCreateTime != nil {
		out.CreatedAt = *in.InstanceCreateTime
	}
	if in.Endpoint != nil && out.Status == domain.StatusAvailable {
		out.Connection = domain.Connection{
			Host:           awsapi.ToString(in.Endpoint.Address),
			Port:           int(awsapi.ToInt32(in.Endpoint.Port)),
			MasterUsername: awsapi.ToString(in.MasterUsername),
			DatabaseName:   awsapi.ToString(in.DBName),
			EngineVersion:  out.EngineVersion,
		}
	}
	return out
}

func (b *Backend) snapshotToDomain(s *rdstypes.DBSnapshot) domain.Snapshot {
	out := domain.Snapshot{
		ID:            awsapi.ToString(s.DBSnapshotIdentifier),
		Instance:      awsapi.ToString(s.DBInstanceIdentifier),
		Engine:        domainEngine(awsapi.ToString(s.Engine)),
		EngineVersion: awsapi.ToString(s.EngineVersion),
		Status:        snapshotStatus(awsapi.ToString(s.Status)),
	}
	if s.SnapshotCreateTime != nil {
		out.CreatedAt = *s.SnapshotCreateTime
	}
	return out
}

// ----------------------------------------------------------------------
// Instances
// ----------------------------------------------------------------------

func (b *Backend) CreateInstance(ctx context.Context, name string, opt domain.CreateInstanceOptions) (domain.CreateInstanceResult, error) {
	in := &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: awsapi.String(name),
		Engine:               awsapi.String(awsEngine(opt.Engine)),
		EngineVersion:        awsapi.String(opt.EngineVersion),
		DBInstanceClass:      awsapi.String(opt.InstanceClass),
		MasterUsername:       awsapi.String(opt.MasterUsername),
		AllocatedStorage:     awsapi.Int32(int32(opt.AllocatedStorageGB)),
	}
	revealed := ""
	if opt.MasterPassword != "" {
		in.MasterUserPassword = awsapi.String(opt.MasterPassword)
	} else {
		// AWS RDS supports ManageMasterUserPassword + Secrets Manager
		// integration; the shim sticks to the explicit-password path
		// since not every backend has SM. Generate a password locally.
		pw := newPassword()
		in.MasterUserPassword = awsapi.String(pw)
		revealed = pw
	}
	out, err := b.c.CreateDBInstance(ctx, in)
	if err != nil {
		return domain.CreateInstanceResult{}, translateErr(err, "instance", name)
	}
	return domain.CreateInstanceResult{
		Instance:       b.instanceToDomain(out.DBInstance),
		MasterPassword: revealed,
	}, nil
}

func (b *Backend) DeleteInstance(ctx context.Context, name string) error {
	_, err := b.c.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: awsapi.String(name),
		SkipFinalSnapshot:    awsapi.Bool(true),
	})
	if err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

func (b *Backend) DescribeInstance(ctx context.Context, name string) (domain.Instance, error) {
	out, err := b.c.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: awsapi.String(name),
	})
	if err != nil {
		return domain.Instance{}, translateErr(err, "instance", name)
	}
	if len(out.DBInstances) == 0 {
		return domain.Instance{}, domain.NoSuchInstance(name)
	}
	return b.instanceToDomain(&out.DBInstances[0]), nil
}

func (b *Backend) ListInstances(ctx context.Context, opt domain.ListInstancesOptions) (domain.ListInstancesResult, error) {
	out, err := b.c.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		return domain.ListInstancesResult{}, translateErr(err, "instance", "")
	}
	res := domain.ListInstancesResult{}
	for i := range out.DBInstances {
		name := awsapi.ToString(out.DBInstances[i].DBInstanceIdentifier)
		if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
			continue
		}
		res.Instances = append(res.Instances, b.instanceToDomain(&out.DBInstances[i]))
		if opt.MaxResults > 0 && len(res.Instances) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) ModifyInstance(ctx context.Context, name string, opt domain.ModifyInstanceOptions) error {
	in := &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier: awsapi.String(name),
		ApplyImmediately:     awsapi.Bool(true),
	}
	if opt.AllocatedStorageGB > 0 {
		in.AllocatedStorage = awsapi.Int32(int32(opt.AllocatedStorageGB))
	}
	if opt.InstanceClass != "" {
		in.DBInstanceClass = awsapi.String(opt.InstanceClass)
	}
	if opt.MasterPassword != "" {
		in.MasterUserPassword = awsapi.String(opt.MasterPassword)
	}
	_, err := b.c.ModifyDBInstance(ctx, in)
	if err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

func (b *Backend) RebootInstance(ctx context.Context, name string) error {
	_, err := b.c.RebootDBInstance(ctx, &rds.RebootDBInstanceInput{
		DBInstanceIdentifier: awsapi.String(name),
	})
	if err != nil {
		return translateErr(err, "instance", name)
	}
	return nil
}

// ----------------------------------------------------------------------
// Snapshots
// ----------------------------------------------------------------------

func (b *Backend) CreateSnapshot(ctx context.Context, instance, snapshotID string) (domain.Snapshot, error) {
	out, err := b.c.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
		DBInstanceIdentifier: awsapi.String(instance),
		DBSnapshotIdentifier: awsapi.String(snapshotID),
	})
	if err != nil {
		return domain.Snapshot{}, translateErr(err, "snapshot", snapshotID)
	}
	return b.snapshotToDomain(out.DBSnapshot), nil
}

func (b *Backend) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	_, err := b.c.DeleteDBSnapshot(ctx, &rds.DeleteDBSnapshotInput{
		DBSnapshotIdentifier: awsapi.String(snapshotID),
	})
	if err != nil {
		return translateErr(err, "snapshot", snapshotID)
	}
	return nil
}

func (b *Backend) DescribeSnapshot(ctx context.Context, snapshotID string) (domain.Snapshot, error) {
	out, err := b.c.DescribeDBSnapshots(ctx, &rds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: awsapi.String(snapshotID),
	})
	if err != nil {
		return domain.Snapshot{}, translateErr(err, "snapshot", snapshotID)
	}
	if len(out.DBSnapshots) == 0 {
		return domain.Snapshot{}, domain.NoSuchSnapshot(snapshotID)
	}
	return b.snapshotToDomain(&out.DBSnapshots[0]), nil
}

func (b *Backend) ListSnapshots(ctx context.Context, opt domain.ListSnapshotsOptions) (domain.ListSnapshotsResult, error) {
	in := &rds.DescribeDBSnapshotsInput{}
	if opt.Instance != "" {
		in.DBInstanceIdentifier = awsapi.String(opt.Instance)
	}
	out, err := b.c.DescribeDBSnapshots(ctx, in)
	if err != nil {
		return domain.ListSnapshotsResult{}, translateErr(err, "snapshot", "")
	}
	res := domain.ListSnapshotsResult{}
	for i := range out.DBSnapshots {
		res.Snapshots = append(res.Snapshots, b.snapshotToDomain(&out.DBSnapshots[i]))
		if opt.MaxResults > 0 && len(res.Snapshots) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) RestoreFromSnapshot(ctx context.Context, snapshotID, newInstanceName string) (domain.CreateInstanceResult, error) {
	out, err := b.c.RestoreDBInstanceFromDBSnapshot(ctx, &rds.RestoreDBInstanceFromDBSnapshotInput{
		DBInstanceIdentifier: awsapi.String(newInstanceName),
		DBSnapshotIdentifier: awsapi.String(snapshotID),
	})
	if err != nil {
		return domain.CreateInstanceResult{}, translateErr(err, "instance", newInstanceName)
	}
	return domain.CreateInstanceResult{Instance: b.instanceToDomain(out.DBInstance)}, nil
}

// ----------------------------------------------------------------------
// Error mapping
// ----------------------------------------------------------------------

func translateErr(err error, kind, name string) error {
	var notFound *rdstypes.DBInstanceNotFoundFault
	if errors.As(err, &notFound) {
		return domain.NoSuchInstance(name)
	}
	var alreadyExists *rdstypes.DBInstanceAlreadyExistsFault
	if errors.As(err, &alreadyExists) {
		return domain.InstanceAlreadyExists(name)
	}
	var snapNotFound *rdstypes.DBSnapshotNotFoundFault
	if errors.As(err, &snapNotFound) {
		return domain.NoSuchSnapshot(name)
	}
	var snapExists *rdstypes.DBSnapshotAlreadyExistsFault
	if errors.As(err, &snapExists) {
		return domain.SnapshotAlreadyExists(name)
	}
	return fmt.Errorf("rds %s %q: %w", kind, name, err)
}

func newPassword() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
