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
)

type Server struct {
	s domain.RDBMS
}

func New(s domain.RDBMS) *Server { return &Server{s: s} }

var (
	reInstances       = regexp.MustCompile(`^/v1/projects/([^/]+)/instances/?$`)
	reInstance        = regexp.MustCompile(`^/v1/projects/([^/]+)/instances/([^/]+)$`)
	reInstanceRestart = regexp.MustCompile(`^/v1/projects/([^/]+)/instances/([^/]+)/restart$`)
	reInstanceRestore = regexp.MustCompile(`^/v1/projects/([^/]+)/instances/([^/]+)/restoreBackup$`)
	reBackupRuns      = regexp.MustCompile(`^/v1/projects/([^/]+)/instances/([^/]+)/backupRuns/?$`)
	reBackupRun       = regexp.MustCompile(`^/v1/projects/([^/]+)/instances/([^/]+)/backupRuns/([^/]+)$`)
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
		State:           gcpStatusFromDomain(in.Status),
		Settings: &sqladmin.Settings{
			Tier:           in.InstanceClass,
			DataDiskSizeGb: int64(in.AllocatedStorageGB),
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

func writeOperation(w http.ResponseWriter, target, opType string) {
	writeJSON(w, http.StatusOK, &sqladmin.Operation{
		Name:          fmt.Sprintf("op-%d", time.Now().UnixNano()),
		OperationType: opType,
		TargetId:      target,
		Status:        "PENDING",
	})
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
