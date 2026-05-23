// Package gcp_cloudsql is the GCP Cloud SQL Admin REST/JSON
// frontend for shimanism's rdbms service. Reuses
// `google.golang.org/api/sqladmin/v1` wire types per the
// reuse-over-reinvention rule.
//
// Cloud SQL Admin is async on every mutating op — Create / Delete /
// Patch / Restart all return an `Operation` resource (a long-running
// op the client polls separately). The shim returns an `Operation`
// envelope with status=PENDING; clients poll Operations.Get to
// observe progress (out of intersection at this phase — most SDK
// clients also poll `Instances.Get`, which the shim implements
// honestly).
package gcp_cloudsql

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/e6qu/shimanism/internal/rdbms/domain"

	_ "github.com/e6qu/shimanism/services/rdbms/gen/gcp" // Phase 13.B spec-drift contract; gen.gcp.Routes is the canonical route inventory.
)

type Server struct {
	s domain.RDBMS
}

func New(s domain.RDBMS) *Server { return &Server{s: s} }

// Cloud SQL Admin URL families:
//
//	/v1/projects/{p}/... — google.golang.org/api/sqladmin/v1
//	  (the Go SDK + gcloud's default endpoint)
//	/sql/v1beta4/projects/{p}/... — hashicorp/google provider
//	  (the `google_sql_database_instance` resource targets this)
//
// Both shapes route to the same handler. Phase 10.3 close of BUG-16.
const sqlPathPrefix = `^/(?:v1|sql/v1beta4)`

var (
	reInstances       = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/?$`)
	reInstance        = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/([^/]+)$`)
	reInstanceRestart = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/([^/]+)/restart$`)
	reInstanceRestore = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/([^/]+)/restoreBackup$`)
	reBackupRuns      = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/([^/]+)/backupRuns/?$`)
	reBackupRun       = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/([^/]+)/backupRuns/([^/]+)$`)
	reInstanceUsers   = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/([^/]+)/users/?$`)
	reInstanceDBs     = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/instances/([^/]+)/databases/?$`)
	reOperation       = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/operations/([^/]+)$`)
	reOperations      = regexp.MustCompile(sqlPathPrefix + `/projects/([^/]+)/operations/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reInstanceRestart.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.restart(w, r, m[2])
		return
	}
	if m := reInstanceRestore.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.restoreBackup(w, r, m[2])
		return
	}
	if m := reBackupRun.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getBackupRun(w, r, m[2], m[3])
		case http.MethodDelete:
			srv.deleteBackupRun(w, r, m[2], m[3])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on backup run")
		}
		return
	}
	if m := reBackupRuns.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.listBackupRuns(w, r, m[2])
		case http.MethodPost:
			srv.createBackupRun(w, r, m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on backup runs")
		}
		return
	}
	if m := reInstanceUsers.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		// hashicorp/google's google_sql_database_instance reads the
		// users list during plan refresh. The cross-cloud intersection
		// doesn't expose Cloud SQL Users (per-engine auth is
		// engine-specific and not part of the rdbms domain). Return
		// the canonical "no users" envelope — real GCP returns the
		// same shape for an instance with only the master user, which
		// the provider's schema treats as out-of-state. Category 2
		// (feature unset).
		srv.listInstanceUsers(w, r, m[2])
		return
	}
	if m := reInstanceDBs.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		// Same category as Users: provider refresh reads the
		// databases list; cross-cloud intersection only models the
		// instance-level "initial database" (Connection.DatabaseName).
		// Return a single-item list with the initial database so the
		// provider's state-walking doesn't propose a recreate.
		srv.listInstanceDatabases(w, r, m[2])
		return
	}
	if m := reInstance.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getInstance(w, r, m[2])
		case http.MethodDelete:
			srv.deleteInstance(w, r, m[2])
		case http.MethodPatch, http.MethodPut:
			srv.patchInstance(w, r, m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on instance")
		}
		return
	}
	if reInstances.MatchString(path) {
		switch method {
		case http.MethodGet:
			srv.listInstances(w, r)
			return
		case http.MethodPost:
			srv.createInstance(w, r)
			return
		}
	}
	if m := reOperation.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.getOperation(w, r, m[2])
		return
	}
	if reOperations.MatchString(path) && method == http.MethodGet {
		srv.listOperations(w, r)
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND",
		"no Cloud SQL Admin route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Instances
// ----------------------------------------------------------------------

func (srv *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	var body sqladmin.DatabaseInstance
	if !decodeJSON(w, r, &body) {
		return
	}
	engine := engineFromVersion(body.DatabaseVersion)
	opt := domain.CreateInstanceOptions{
		Engine:         engine,
		EngineVersion:  body.DatabaseVersion,
		MasterUsername: "shimadmin",
		MasterPassword: body.RootPassword,
		Region:         body.Region,
	}
	if body.Settings != nil {
		opt.AllocatedStorageGB = int(body.Settings.DataDiskSizeGb)
		opt.InstanceClass = body.Settings.Tier
	}
	if _, err := srv.s.CreateInstance(r.Context(), body.Name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	// Cloud SQL returns an Operation, not the instance itself.
	writeOperation(w, body.Name, "CREATE")
}

func (srv *Server) getInstance(w http.ResponseWriter, r *http.Request, name string) {
	inst, err := srv.s.DescribeInstance(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceToGCP(inst))
}

func (srv *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := &sqladmin.InstancesListResponse{}
	for _, i := range res.Instances {
		out.Items = append(out.Items, instanceToGCP(i))
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteInstance(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name, "DELETE")
}

func (srv *Server) patchInstance(w http.ResponseWriter, r *http.Request, name string) {
	var body sqladmin.DatabaseInstance
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ModifyInstanceOptions{}
	if body.Settings != nil {
		opt.AllocatedStorageGB = int(body.Settings.DataDiskSizeGb)
		opt.InstanceClass = body.Settings.Tier
	}
	if body.RootPassword != "" {
		opt.MasterPassword = body.RootPassword
	}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name, "UPDATE")
}

func (srv *Server) restart(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name, "RESTART")
}

// ----------------------------------------------------------------------
// Backup runs (snapshots)
// ----------------------------------------------------------------------

func (srv *Server) createBackupRun(w http.ResponseWriter, r *http.Request, instance string) {
	var body sqladmin.BackupRun
	if !decodeJSON(w, r, &body) {
		return
	}
	snapID := body.Description
	if snapID == "" {
		snapID = fmt.Sprintf("backup-%d", time.Now().UnixNano())
	}
	if _, err := srv.s.CreateSnapshot(r.Context(), instance, snapID); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, instance, "BACKUP_VOLUME")
}

func (srv *Server) getBackupRun(w http.ResponseWriter, r *http.Request, instance, id string) {
	snap, err := srv.s.DescribeSnapshot(r.Context(), id)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshotToGCP(snap))
}

func (srv *Server) deleteBackupRun(w http.ResponseWriter, r *http.Request, instance, id string) {
	if err := srv.s.DeleteSnapshot(r.Context(), id); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, instance, "DELETE_BACKUP")
}

// listInstanceUsers returns the canonical "no users" envelope. Real
// Cloud SQL exposes per-engine user accounts which the cross-cloud
// rdbms intersection doesn't model (Postgres / MySQL / SQL Server
// differ enough that the shim only models the instance-level master
// credential). Provider-refresh reads of this list see an empty
// response and treat that as the resource state of record. Phase
// 10.3 close of BUG-16.
func (srv *Server) listInstanceUsers(w http.ResponseWriter, r *http.Request, instance string) {
	if _, err := srv.s.DescribeInstance(r.Context(), instance); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &sqladmin.UsersListResponse{})
}

// listInstanceDatabases surfaces the single "initial" database from
// the Connection metadata. Cross-cloud intersection only models one
// database per instance at create time (Connection.DatabaseName).
// Provider reads this list to detect drift on the instance's
// database set.
func (srv *Server) listInstanceDatabases(w http.ResponseWriter, r *http.Request, instance string) {
	inst, err := srv.s.DescribeInstance(r.Context(), instance)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := &sqladmin.DatabasesListResponse{}
	if inst.Connection.DatabaseName != "" {
		out.Items = append(out.Items, &sqladmin.Database{
			Name:     inst.Connection.DatabaseName,
			Instance: instance,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) listBackupRuns(w http.ResponseWriter, r *http.Request, instance string) {
	res, err := srv.s.ListSnapshots(r.Context(), domain.ListSnapshotsOptions{Instance: instance})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := &sqladmin.BackupRunsListResponse{}
	for _, s := range res.Snapshots {
		out.Items = append(out.Items, snapshotToGCP(s))
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) restoreBackup(w http.ResponseWriter, r *http.Request, instance string) {
	var body sqladmin.InstancesRestoreBackupRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.RestoreBackupContext == nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "restoreBackupContext is required")
		return
	}
	snapID := fmt.Sprintf("%d", body.RestoreBackupContext.BackupRunId)
	if _, err := srv.s.RestoreFromSnapshot(r.Context(), snapID, instance); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, instance, "RESTORE_VOLUME")
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func engineFromVersion(v string) domain.Engine {
	switch {
	case strings.HasPrefix(v, "POSTGRES_"):
		return domain.EnginePostgres
	case strings.HasPrefix(v, "MYSQL_"):
		return domain.EngineMySQL
	default:
		return domain.EngineUnknown
	}
}

func gcpStatusFromDomain(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "RUNNABLE"
	case domain.StatusCreating:
		return "PENDING_CREATE"
	case domain.StatusModifying:
		return "PENDING_UPDATE"
	case domain.StatusRebooting:
		return "PENDING_UPDATE"
	case domain.StatusDeleting:
		return "PENDING_DELETE"
	default:
		return "UNKNOWN_STATE"
	}
}

func instanceToGCP(in domain.Instance) *sqladmin.DatabaseInstance {
	out := &sqladmin.DatabaseInstance{
		Name:            in.Name,
		DatabaseVersion: in.EngineVersion,
		Region:          in.Region,
		State:           gcpStatusFromDomain(in.Status),
		// InstanceType is GCP-specific and provider-required; real
		// GCP defaults to CLOUD_SQL_INSTANCE for first-class instances.
		InstanceType: "CLOUD_SQL_INSTANCE",
		Settings: &sqladmin.Settings{
			Tier:           in.InstanceClass,
			DataDiskSizeGb: int64(in.AllocatedStorageGB),
			// The remainder are Cloud SQL's canonical defaults for an
			// instance that didn't customize them. Real GCP returns
			// these on Read; hashicorp/google's schema expects them
			// in state. Honest defaults — they accurately describe
			// the instance's "default settings" posture, not fakes.
			DataDiskType:         "PD_SSD",
			ActivationPolicy:     "ALWAYS",
			AvailabilityType:     "ZONAL",
			StorageAutoResize:    boolPtr(true),
			PricingPlan:          "PER_USE",
			ReplicationType:      "SYNCHRONOUS",
			ConnectorEnforcement: "NOT_REQUIRED",
		},
	}
	if !in.CreatedAt.IsZero() {
		out.CreateTime = in.CreatedAt.UTC().Format(time.RFC3339)
	}
	if in.Status == domain.StatusAvailable && in.Connection.Host != "" {
		out.IpAddresses = []*sqladmin.IpMapping{
			{Type: "PRIMARY", IpAddress: in.Connection.Host},
		}
	}
	return out
}

func boolPtr(b bool) *bool { return &b }

func snapshotToGCP(s domain.Snapshot) *sqladmin.BackupRun {
	out := &sqladmin.BackupRun{
		Description: s.ID,
		Status:      backupStatusString(s.Status),
	}
	if !s.CreatedAt.IsZero() {
		out.EndTime = s.CreatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func backupStatusString(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "SUCCESSFUL"
	case domain.StatusCreating:
		return "RUNNING"
	default:
		return "ENQUEUED"
	}
}

// writeOperation returns the canonical Operation envelope. The
// Operation Name encodes the opType + target so a later
// Operations.Get call can resolve the current state by reading the
// target resource — stateless, no shim-side operation table.
func writeOperation(w http.ResponseWriter, target, opType string) {
	writeJSON(w, http.StatusOK, &sqladmin.Operation{
		Name:          encodeOperationName(opType, target),
		OperationType: opType,
		TargetId:      target,
		Status:        "PENDING",
	})
}

// encodeOperationName packs (opType, target) into a single string
// safe as an Operation.Name. Format: "op-<opType>-<target>".
// Operations.Get reverses this to find the target resource.
func encodeOperationName(opType, target string) string {
	return "op-" + strings.ToLower(opType) + "-" + target
}

// decodeOperationName unpacks the Operation.Name produced by
// encodeOperationName. Returns (opType, target, ok). For names the
// shim didn't produce (foreign IDs, malformed strings), returns
// ok=false; the caller surfaces a 404.
func decodeOperationName(name string) (opType, target string, ok bool) {
	rest, found := strings.CutPrefix(name, "op-")
	if !found {
		return "", "", false
	}
	// opType is one of create/delete/update/restart/restorebackup.
	for _, t := range []string{"create", "delete", "update", "restart", "restorebackup"} {
		if suf, ok := strings.CutPrefix(rest, t+"-"); ok {
			return t, suf, true
		}
	}
	return "", "", false
}

func (srv *Server) getOperation(w http.ResponseWriter, r *http.Request, name string) {
	opType, target, ok := decodeOperationName(name)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"operation "+name+" not found (shim only resolves operations it issued)")
		return
	}
	inst, err := srv.s.DescribeInstance(r.Context(), target)
	op := &sqladmin.Operation{
		Name:          name,
		OperationType: strings.ToUpper(opType),
		TargetId:      target,
	}
	if err != nil {
		// For delete ops, NoSuchInstance is the success signal —
		// the resource has been destroyed.
		if opType == "delete" {
			op.Status = "DONE"
			writeJSON(w, http.StatusOK, op)
			return
		}
		// Other op types: instance missing means something's wrong.
		mapDomainError(w, err)
		return
	}
	// Resource exists. Map its current status to operation status.
	switch inst.Status {
	case domain.StatusAvailable:
		op.Status = "DONE"
	case domain.StatusCreating, domain.StatusModifying, domain.StatusRebooting, domain.StatusDeleting:
		op.Status = "RUNNING"
	default:
		op.Status = "PENDING"
	}
	writeJSON(w, http.StatusOK, op)
}

func (srv *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	// The shim doesn't keep an operation log — there's no honest
	// way to list past operations without persistent state. Real
	// Cloud SQL Admin returns the recent ops; the shim's intersection
	// is "Operations.Get only" (the SDK + Terraform polling paths
	// only call Get, never List). Return empty list honestly.
	_ = r
	writeJSON(w, http.StatusOK, &sqladmin.OperationsListResponse{Items: nil})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
