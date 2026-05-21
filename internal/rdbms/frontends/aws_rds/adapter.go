// Package aws_rds is the AWS RDS frontend for shimanism's rdbms
// service. Phase 11.11 migrated it from a hand-written awsQuery
// wire layer to spec-driven generated stubs.
package aws_rds

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/e6qu/shimanism/internal/awsquery"
	"github.com/e6qu/shimanism/internal/rdbms/domain"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/rdbms/gen"
)

// Adapter binds gen.RDSBackend to a domain.RDBMS backend.
type Adapter struct {
	s domain.RDBMS
}

// New returns the http.Handler dispatching through the generated
// awsQuery router into the adapter bound to the given backend.
// SigV4 verification is wired in; SHIMANISM_TEST_UNAUTHENTICATED=1
// short-circuits during the conformance-lane rewrite.
func New(s domain.RDBMS) http.Handler {
	router := gen.RegisterRDSRoutes(&Adapter{s: s})
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "rds", Region: "us-east-1"})
	emitErr := func(w http.ResponseWriter, status int, errorType, message string) {
		awsquery.WriteError(w, status, "Sender", errorType, message)
	}
	return sigv4verifier.Middleware(verifier, emitErr)(router)
}

func strPtr(s string) *string { return &s }
func i32Ptr(v int32) *int32   { return &v }
func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// rdsRegion + rdsAccount are stamped into the ARN strings real RDS
// responses include. Real AWS uses the caller's account + region;
// the shim has neither, so a fixed placeholder lets ARN-based
// importers continue working.
const (
	rdsRegion  = "us-east-1"
	rdsAccount = "000000000000"
)

func instanceARN(name string) string {
	return "arn:aws:rds:" + rdsRegion + ":" + rdsAccount + ":db:" + name
}

func dbiResourceID(name string) string { return "db-" + name }

func awsEngineToDomain(s string) domain.Engine {
	switch s {
	case "postgres", "aurora-postgresql":
		return domain.EnginePostgres
	case "mysql", "aurora-mysql":
		return domain.EngineMySQL
	default:
		return domain.EngineUnknown
	}
}

func domainEngineToAWS(e domain.Engine) string {
	switch e {
	case domain.EnginePostgres:
		return "postgres"
	case domain.EngineMySQL:
		return "mysql"
	default:
		return "unknown"
	}
}

func awsStatusFromDomain(s domain.Status) string {
	switch s {
	case domain.StatusCreating:
		return "creating"
	case domain.StatusAvailable:
		return "available"
	case domain.StatusModifying:
		return "modifying"
	case domain.StatusRebooting:
		return "rebooting"
	case domain.StatusDeleting:
		return "deleting"
	default:
		return "unknown"
	}
}

func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return &awsquery.BackendError{HTTPStatus: http.StatusInternalServerError, Type: "Receiver", Code: "InternalFailure", Message: err.Error()}
	}
	be := &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Message: de.Error()}
	switch de.Kind {
	case domain.KindNoSuchInstance:
		be.HTTPStatus = http.StatusNotFound
		be.Code = "DBInstanceNotFound"
	case domain.KindInstanceAlreadyExists:
		be.Code = "DBInstanceAlreadyExists"
	case domain.KindNoSuchSnapshot:
		be.HTTPStatus = http.StatusNotFound
		be.Code = "DBSnapshotNotFound"
	case domain.KindSnapshotAlreadyExists:
		be.Code = "DBSnapshotAlreadyExists"
	case domain.KindInvalidArgument:
		be.Code = "InvalidParameterValue"
	default:
		be.HTTPStatus = http.StatusInternalServerError
		be.Type = "Receiver"
		be.Code = "InternalFailure"
	}
	return be
}

// instanceToAWS renders a domain.Instance as the generated DBInstance
// type. Mirrors the field surface the existing hand-written wire
// emitted; SDK clients dispatch on DBInstanceArn + DbiResourceId so
// the synthesised ARN + ID strings preserve importer compatibility.
func instanceToAWS(inst domain.Instance) *gen.DBInstance {
	arn := instanceARN(inst.Name)
	dbiID := dbiResourceID(inst.Name)
	engine := domainEngineToAWS(inst.Engine)
	status := awsStatusFromDomain(inst.Status)
	masterUser := inst.Connection.MasterUsername
	dbName := inst.Connection.DatabaseName
	out := &gen.DBInstance{
		DBInstanceIdentifier: &inst.Name,
		DBInstanceArn:        &arn,
		DbiResourceId:        &dbiID,
		Engine:               &engine,
		EngineVersion:        &inst.EngineVersion,
		DBInstanceStatus:     &status,
		DBInstanceClass:      &inst.InstanceClass,
		AllocatedStorage:     i32Ptr(int32(inst.AllocatedStorageGB)),
		MasterUsername:       &masterUser,
		DBName:               &dbName,
	}
	if inst.Status == domain.StatusAvailable && inst.Connection.Host != "" {
		out.Endpoint = &gen.Endpoint{
			Address: &inst.Connection.Host,
			Port:    i32Ptr(int32(inst.Connection.Port)),
		}
	}
	return out
}

func snapshotToAWS(s domain.Snapshot) *gen.DBSnapshot {
	engine := domainEngineToAWS(s.Engine)
	status := awsStatusFromDomain(s.Status)
	return &gen.DBSnapshot{
		DBSnapshotIdentifier: &s.ID,
		DBInstanceIdentifier: &s.Instance,
		Engine:               &engine,
		EngineVersion:        &s.EngineVersion,
		Status:               &status,
	}
}

// ---------------------------------------------------------------------
// Instance ops.
// ---------------------------------------------------------------------

func (a *Adapter) CreateDBInstance(ctx context.Context, in *gen.CreateDBInstanceMessage) (*gen.CreateDBInstanceResult, error) {
	name := in.DBInstanceIdentifier
	if name == "" {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameterValue", Message: "DBInstanceIdentifier is required"}
	}
	engineStr := in.Engine
	engine := awsEngineToDomain(engineStr)
	if engine == domain.EngineUnknown {
		return nil, &awsquery.BackendError{HTTPStatus: http.StatusBadRequest, Type: "Sender", Code: "InvalidParameterValue", Message: "unsupported Engine: " + engineStr}
	}
	storage := 0
	if in.AllocatedStorage != nil {
		storage = int(*in.AllocatedStorage)
	}
	opt := domain.CreateInstanceOptions{
		Engine:             engine,
		EngineVersion:      strDeref(in.EngineVersion),
		MasterUsername:     strDeref(in.MasterUsername),
		MasterPassword:     strDeref(in.MasterUserPassword),
		AllocatedStorageGB: storage,
		InstanceClass:      in.DBInstanceClass,
	}
	res, err := a.s.CreateInstance(ctx, name, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateDBInstanceResult{DBInstance: instanceToAWS(res.Instance)}, nil
}

func (a *Adapter) DeleteDBInstance(ctx context.Context, in *gen.DeleteDBInstanceMessage) (*gen.DeleteDBInstanceResult, error) {
	name := in.DBInstanceIdentifier
	if err := a.s.DeleteInstance(ctx, name); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteDBInstanceResult{DBInstance: instanceToAWS(domain.Instance{
		Name:   name,
		Status: domain.StatusDeleting,
	})}, nil
}

func (a *Adapter) DescribeDBInstances(ctx context.Context, in *gen.DescribeDBInstancesMessage) (*gen.DBInstanceMessage, error) {
	name := strDeref(in.DBInstanceIdentifier)
	out := &gen.DBInstanceMessage{}
	if name != "" {
		inst, err := a.s.DescribeInstance(ctx, name)
		if err != nil {
			return nil, mapDomainErr(err)
		}
		out.DBInstances.Member = append(out.DBInstances.Member, *instanceToAWS(inst))
		return out, nil
	}
	res, err := a.s.ListInstances(ctx, domain.ListInstancesOptions{})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	for _, i := range res.Instances {
		out.DBInstances.Member = append(out.DBInstances.Member, *instanceToAWS(i))
	}
	return out, nil
}

func (a *Adapter) ModifyDBInstance(ctx context.Context, in *gen.ModifyDBInstanceMessage) (*gen.ModifyDBInstanceResult, error) {
	name := in.DBInstanceIdentifier
	opt := domain.ModifyInstanceOptions{
		InstanceClass:  strDeref(in.DBInstanceClass),
		MasterPassword: strDeref(in.MasterUserPassword),
	}
	if in.AllocatedStorage != nil {
		opt.AllocatedStorageGB = int(*in.AllocatedStorage)
	}
	if err := a.s.ModifyInstance(ctx, name, opt); err != nil {
		return nil, mapDomainErr(err)
	}
	inst, _ := a.s.DescribeInstance(ctx, name)
	return &gen.ModifyDBInstanceResult{DBInstance: instanceToAWS(inst)}, nil
}

func (a *Adapter) RebootDBInstance(ctx context.Context, in *gen.RebootDBInstanceMessage) (*gen.RebootDBInstanceResult, error) {
	name := in.DBInstanceIdentifier
	if err := a.s.RebootInstance(ctx, name); err != nil {
		return nil, mapDomainErr(err)
	}
	inst, _ := a.s.DescribeInstance(ctx, name)
	return &gen.RebootDBInstanceResult{DBInstance: instanceToAWS(inst)}, nil
}

// ---------------------------------------------------------------------
// Snapshot ops.
// ---------------------------------------------------------------------

func (a *Adapter) CreateDBSnapshot(ctx context.Context, in *gen.CreateDBSnapshotMessage) (*gen.CreateDBSnapshotResult, error) {
	inst := in.DBInstanceIdentifier
	id := in.DBSnapshotIdentifier
	snap, err := a.s.CreateSnapshot(ctx, inst, id)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateDBSnapshotResult{DBSnapshot: snapshotToAWS(snap)}, nil
}

func (a *Adapter) DeleteDBSnapshot(ctx context.Context, in *gen.DeleteDBSnapshotMessage) (*gen.DeleteDBSnapshotResult, error) {
	id := in.DBSnapshotIdentifier
	snap, derr := a.s.DescribeSnapshot(ctx, id)
	if err := a.s.DeleteSnapshot(ctx, id); err != nil {
		return nil, mapDomainErr(err)
	}
	if derr == nil {
		return &gen.DeleteDBSnapshotResult{DBSnapshot: snapshotToAWS(snap)}, nil
	}
	return &gen.DeleteDBSnapshotResult{DBSnapshot: snapshotToAWS(domain.Snapshot{ID: id, Status: domain.StatusDeleting})}, nil
}

func (a *Adapter) DescribeDBSnapshots(ctx context.Context, in *gen.DescribeDBSnapshotsMessage) (*gen.DBSnapshotMessage, error) {
	id := strDeref(in.DBSnapshotIdentifier)
	out := &gen.DBSnapshotMessage{}
	if id != "" {
		snap, err := a.s.DescribeSnapshot(ctx, id)
		if err != nil {
			return nil, mapDomainErr(err)
		}
		out.DBSnapshots.Member = append(out.DBSnapshots.Member, *snapshotToAWS(snap))
		return out, nil
	}
	res, err := a.s.ListSnapshots(ctx, domain.ListSnapshotsOptions{Instance: strDeref(in.DBInstanceIdentifier)})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	for _, s := range res.Snapshots {
		out.DBSnapshots.Member = append(out.DBSnapshots.Member, *snapshotToAWS(s))
	}
	return out, nil
}

func (a *Adapter) RestoreDBInstanceFromDBSnapshot(ctx context.Context, in *gen.RestoreDBInstanceFromDBSnapshotMessage) (*gen.RestoreDBInstanceFromDBSnapshotResult, error) {
	id := strDeref(in.DBSnapshotIdentifier)
	name := in.DBInstanceIdentifier
	res, err := a.s.RestoreFromSnapshot(ctx, id, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.RestoreDBInstanceFromDBSnapshotResult{DBInstance: instanceToAWS(res.Instance)}, nil
}

// strconv import keep — avoids the unused-import warning when the
// generated stubs are extended in future without adding new int-parse
// call sites in this adapter.
var _ = strconv.Itoa
