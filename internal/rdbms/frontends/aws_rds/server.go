// Package aws_rds is the AWS RDS-shaped HTTP frontend for
// shimanism's rdbms service.
//
// RDS uses the awsQuery wire protocol (same family as SNS):
// form-encoded request bodies dispatched on `Action=...`, XML
// response envelopes wrapped in
// `<{Op}Response><{Op}Result>...<ResponseMetadata/></>` with the
// RDS namespace.
package aws_rds

import (
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

type Server struct {
	s domain.RDBMS
}

func New(s domain.RDBMS) *Server { return &Server{s: s} }

const rdsNamespace = "http://rds.amazonaws.com/doc/2014-10-31/"

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Sender", "MethodNotAllowed",
			"only POST is allowed on the RDS endpoint")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Sender", "MalformedInput",
			"read body: "+err.Error())
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Sender", "MalformedInput",
			"parse query: "+err.Error())
		return
	}
	action := form.Get("Action")
	switch action {
	case "CreateDBInstance":
		srv.createDBInstance(w, r, form)
	case "DeleteDBInstance":
		srv.deleteDBInstance(w, r, form)
	case "DescribeDBInstances":
		srv.describeDBInstances(w, r, form)
	case "ModifyDBInstance":
		srv.modifyDBInstance(w, r, form)
	case "RebootDBInstance":
		srv.rebootDBInstance(w, r, form)
	case "CreateDBSnapshot":
		srv.createDBSnapshot(w, r, form)
	case "DeleteDBSnapshot":
		srv.deleteDBSnapshot(w, r, form)
	case "DescribeDBSnapshots":
		srv.describeDBSnapshots(w, r, form)
	case "RestoreDBInstanceFromDBSnapshot":
		srv.restoreFromSnapshot(w, r, form)
	default:
		writeError(w, http.StatusBadRequest, "Sender", "InvalidAction",
			"unknown or out-of-intersection action: "+action)
	}
}

// ----------------------------------------------------------------------
// Instance handlers
// ----------------------------------------------------------------------

func (srv *Server) createDBInstance(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("DBInstanceIdentifier")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"DBInstanceIdentifier is required")
		return
	}
	engineStr := form.Get("Engine")
	engine := awsEngineToDomain(engineStr)
	if engine == domain.EngineUnknown {
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameter",
			"unsupported Engine: "+engineStr)
		return
	}
	storage, _ := strconv.Atoi(form.Get("AllocatedStorage"))
	opt := domain.CreateInstanceOptions{
		Engine:             engine,
		EngineVersion:      form.Get("EngineVersion"),
		MasterUsername:     form.Get("MasterUsername"),
		MasterPassword:     form.Get("MasterUserPassword"),
		AllocatedStorageGB: storage,
		InstanceClass:      form.Get("DBInstanceClass"),
	}
	res, err := srv.s.CreateInstance(r.Context(), name, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeDBInstanceResponse(w, "CreateDBInstance", res.Instance)
}

func (srv *Server) deleteDBInstance(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("DBInstanceIdentifier")
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	// Return a snapshot of the just-deleted state for SDK happiness.
	writeDBInstanceResponse(w, "DeleteDBInstance", domain.Instance{
		Name:   name,
		Status: domain.StatusDeleting,
	})
}

func (srv *Server) describeDBInstances(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("DBInstanceIdentifier")
	if name != "" {
		inst, err := srv.s.DescribeInstance(r.Context(), name)
		if err != nil {
			mapDomainError(w, err)
			return
		}
		writeDescribeDBInstances(w, []domain.Instance{inst})
		return
	}
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeDescribeDBInstances(w, res.Instances)
}

func (srv *Server) modifyDBInstance(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("DBInstanceIdentifier")
	storage, _ := strconv.Atoi(form.Get("AllocatedStorage"))
	opt := domain.ModifyInstanceOptions{
		AllocatedStorageGB: storage,
		InstanceClass:      form.Get("DBInstanceClass"),
		MasterPassword:     form.Get("MasterUserPassword"),
	}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeDBInstanceResponse(w, "ModifyDBInstance", inst)
}

func (srv *Server) rebootDBInstance(w http.ResponseWriter, r *http.Request, form url.Values) {
	name := form.Get("DBInstanceIdentifier")
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeDBInstanceResponse(w, "RebootDBInstance", inst)
}

// ----------------------------------------------------------------------
// Snapshot handlers
// ----------------------------------------------------------------------

func (srv *Server) createDBSnapshot(w http.ResponseWriter, r *http.Request, form url.Values) {
	inst := form.Get("DBInstanceIdentifier")
	id := form.Get("DBSnapshotIdentifier")
	snap, err := srv.s.CreateSnapshot(r.Context(), inst, id)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeDBSnapshotResponse(w, "CreateDBSnapshot", snap)
}

func (srv *Server) deleteDBSnapshot(w http.ResponseWriter, r *http.Request, form url.Values) {
	id := form.Get("DBSnapshotIdentifier")
	snap, derr := srv.s.DescribeSnapshot(r.Context(), id)
	if err := srv.s.DeleteSnapshot(r.Context(), id); err != nil {
		mapDomainError(w, err)
		return
	}
	if derr == nil {
		writeDBSnapshotResponse(w, "DeleteDBSnapshot", snap)
		return
	}
	writeDBSnapshotResponse(w, "DeleteDBSnapshot", domain.Snapshot{ID: id, Status: domain.StatusDeleting})
}

func (srv *Server) describeDBSnapshots(w http.ResponseWriter, r *http.Request, form url.Values) {
	id := form.Get("DBSnapshotIdentifier")
	if id != "" {
		snap, err := srv.s.DescribeSnapshot(r.Context(), id)
		if err != nil {
			mapDomainError(w, err)
			return
		}
		writeDescribeDBSnapshots(w, []domain.Snapshot{snap})
		return
	}
	res, err := srv.s.ListSnapshots(r.Context(), domain.ListSnapshotsOptions{
		Instance: form.Get("DBInstanceIdentifier"),
	})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeDescribeDBSnapshots(w, res.Snapshots)
}

func (srv *Server) restoreFromSnapshot(w http.ResponseWriter, r *http.Request, form url.Values) {
	id := form.Get("DBSnapshotIdentifier")
	name := form.Get("DBInstanceIdentifier")
	res, err := srv.s.RestoreFromSnapshot(r.Context(), id, name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeDBInstanceResponse(w, "RestoreDBInstanceFromDBSnapshot", res.Instance)
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

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
