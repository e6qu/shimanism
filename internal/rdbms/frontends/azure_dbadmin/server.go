// Package azure_dbadmin is the Azure DB Admin REST frontend for
// shimanism's rdbms service (Postgres only, matching the cnpg
// backend choice).
//
// ARM URL shape:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}
//
// The shim ignores subscription / resource-group scoping at this
// phase — the backend handles the actual ARM addressing — and treats
// {name} as the domain instance name.
//
// Mutating ops in real ARM return 202 + an `Azure-AsyncOperation`
// header pointing at a polling URL. The shim's frontend returns
// 200 with the current instance JSON; Azure SDK pollers eventually
// fall through to a `Get` call which the shim handles honestly.
// This trades wire-shape fidelity for stateless simplicity; SDK
// conformance is deferred for the Azure cell.
package azure_dbadmin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

type Server struct {
	s domain.RDBMS
}

func New(s domain.RDBMS) *Server { return &Server{s: s} }

var (
	reServer = regexp.MustCompile(
		`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.DBforPostgreSQL/flexibleServers/([^/]+)/?$`)
	reServerList = regexp.MustCompile(
		`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.DBforPostgreSQL/flexibleServers/?$`)
	reServerRestart = regexp.MustCompile(
		`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.DBforPostgreSQL/flexibleServers/([^/]+)/restart/?$`)
	reBackup = regexp.MustCompile(
		`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.DBforPostgreSQL/flexibleServers/([^/]+)/backups/([^/]+)/?$`)
	reBackupList = regexp.MustCompile(
		`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.DBforPostgreSQL/flexibleServers/([^/]+)/backups/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reServerRestart.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.restart(w, r, m[1])
		return
	}
	if m := reBackup.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createBackup(w, r, m[1], m[2])
		case http.MethodGet:
			srv.getBackup(w, r, m[1], m[2])
		case http.MethodDelete:
			srv.deleteBackup(w, r, m[1], m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on backup")
		}
		return
	}
	if m := reBackupList.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.listBackups(w, r, m[1])
		return
	}
	if m := reServer.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createServer(w, r, m[1])
		case http.MethodGet:
			srv.getServer(w, r, m[1])
		case http.MethodDelete:
			srv.deleteServer(w, r, m[1])
		case http.MethodPatch:
			srv.patchServer(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on server")
		}
		return
	}
	if reServerList.MatchString(path) && method == http.MethodGet {
		srv.listServers(w, r)
		return
	}

	writeError(w, http.StatusNotFound, "ResourceNotFound",
		"no Azure DBforPostgreSQL route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Wire shapes (subset of the Microsoft.DBforPostgreSQL/flexibleServers schema)
// ----------------------------------------------------------------------

type serverBody struct {
	Location   string            `json:"location,omitempty"`
	SKU        *skuBody          `json:"sku,omitempty"`
	Properties *serverProperties `json:"properties,omitempty"`
}

type skuBody struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type serverProperties struct {
	AdministratorLogin         string       `json:"administratorLogin,omitempty"`
	AdministratorLoginPassword string       `json:"administratorLoginPassword,omitempty"`
	Version                    string       `json:"version,omitempty"`
	State                      string       `json:"state,omitempty"`
	FullyQualifiedDomainName   string       `json:"fullyQualifiedDomainName,omitempty"`
	Storage                    *storageBody `json:"storage,omitempty"`
}

type storageBody struct {
	StorageSizeGB int32 `json:"storageSizeGB,omitempty"`
}

type serverResource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	SKU        *skuBody          `json:"sku,omitempty"`
	Properties *serverProperties `json:"properties"`
}

type listResponse struct {
	Value []*serverResource `json:"value"`
}

type backupResource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Properties map[string]string `json:"properties,omitempty"`
}

type backupList struct {
	Value []*backupResource `json:"value"`
}

// ----------------------------------------------------------------------
// Server handlers
// ----------------------------------------------------------------------

func (srv *Server) createServer(w http.ResponseWriter, r *http.Request, name string) {
	var body serverBody
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.CreateInstanceOptions{
		Engine: domain.EnginePostgres,
	}
	if body.Properties != nil {
		opt.EngineVersion = body.Properties.Version
		opt.MasterUsername = body.Properties.AdministratorLogin
		opt.MasterPassword = body.Properties.AdministratorLoginPassword
		if body.Properties.Storage != nil {
			opt.AllocatedStorageGB = int(body.Properties.Storage.StorageSizeGB)
		}
	}
	if body.SKU != nil {
		opt.InstanceClass = body.SKU.Name
	}
	if _, err := srv.s.CreateInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusCreated, instanceToARM(inst))
}

func (srv *Server) getServer(w http.ResponseWriter, r *http.Request, name string) {
	inst, err := srv.s.DescribeInstance(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) deleteServer(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) patchServer(w http.ResponseWriter, r *http.Request, name string) {
	var body serverBody
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.ModifyInstanceOptions{}
	if body.Properties != nil {
		if body.Properties.AdministratorLoginPassword != "" {
			opt.MasterPassword = body.Properties.AdministratorLoginPassword
		}
		if body.Properties.Storage != nil {
			opt.AllocatedStorageGB = int(body.Properties.Storage.StorageSizeGB)
		}
	}
	if body.SKU != nil {
		opt.InstanceClass = body.SKU.Name
	}
	if err := srv.s.ModifyInstance(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	inst, _ := srv.s.DescribeInstance(r.Context(), name)
	writeJSON(w, http.StatusOK, instanceToARM(inst))
}

func (srv *Server) restart(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.RebootInstance(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) listServers(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListInstances(r.Context(), domain.ListInstancesOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := listResponse{}
	for _, i := range res.Instances {
		out.Value = append(out.Value, instanceToARM(i))
	}
	writeJSON(w, http.StatusOK, &out)
}

// ----------------------------------------------------------------------
// Backup handlers
// ----------------------------------------------------------------------

func (srv *Server) createBackup(w http.ResponseWriter, r *http.Request, instance, id string) {
	if _, err := srv.s.CreateSnapshot(r.Context(), instance, id); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, &backupResource{
		Name: id,
		Type: "Microsoft.DBforPostgreSQL/flexibleServers/backups",
	})
}

func (srv *Server) getBackup(w http.ResponseWriter, r *http.Request, instance, id string) {
	snap, err := srv.s.DescribeSnapshot(r.Context(), id)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &backupResource{
		Name: snap.ID,
		Type: "Microsoft.DBforPostgreSQL/flexibleServers/backups",
		Properties: map[string]string{
			"status": statusToARM(snap.Status),
		},
	})
}

func (srv *Server) deleteBackup(w http.ResponseWriter, r *http.Request, instance, id string) {
	if err := srv.s.DeleteSnapshot(r.Context(), id); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) listBackups(w http.ResponseWriter, r *http.Request, instance string) {
	res, err := srv.s.ListSnapshots(r.Context(), domain.ListSnapshotsOptions{Instance: instance})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := backupList{}
	for _, s := range res.Snapshots {
		out.Value = append(out.Value, &backupResource{
			Name: s.ID,
			Type: "Microsoft.DBforPostgreSQL/flexibleServers/backups",
			Properties: map[string]string{
				"status": statusToARM(s.Status),
			},
		})
	}
	writeJSON(w, http.StatusOK, &out)
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func instanceToARM(in domain.Instance) *serverResource {
	res := &serverResource{
		Name:     in.Name,
		Type:     "Microsoft.DBforPostgreSQL/flexibleServers",
		Location: "eastus",
		SKU:      &skuBody{Name: in.InstanceClass},
		Properties: &serverProperties{
			Version: in.EngineVersion,
			State:   statusToARM(in.Status),
			Storage: &storageBody{StorageSizeGB: int32(in.AllocatedStorageGB)},
		},
	}
	if in.Status == domain.StatusAvailable && in.Connection.Host != "" {
		res.Properties.FullyQualifiedDomainName = in.Connection.Host
		res.Properties.AdministratorLogin = in.Connection.MasterUsername
	}
	return res
}

func statusToARM(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "Ready"
	case domain.StatusCreating:
		return "Provisioning"
	case domain.StatusModifying:
		return "Updating"
	case domain.StatusRebooting:
		return "Restarting"
	case domain.StatusDeleting:
		return "Dropping"
	default:
		return "Unknown"
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var _ = fmt.Sprintf
var _ = strings.HasPrefix
var _ = time.Time{}
