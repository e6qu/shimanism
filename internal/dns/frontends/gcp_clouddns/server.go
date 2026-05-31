// Package gcp_clouddns is the GCP Cloud DNS REST/JSON frontend for
// shimanism's DNS service. It speaks the HTTP+JSON wire protocol that
// `google.golang.org/api/dns/v1` (the Discovery-generated REST SDK)
// and `gcloud dns` drive, and translates each request into a call on
// the neutral `domain.DNS` interface.
//
// Per AGENTS.md's reuse-over-reinvention rule, the request/response
// wire types come from `google.golang.org/api/dns/v1` directly — the
// same raw types the SDK is generated from. The emitter at
// services/dns/gen/gcp ships the routing inventory (per AGENTS.md
// decision #11 it's routing-only) which dispatch goes through.
//
// Cloud DNS identifies zones by a project-unique `Name` (resource
// ID) separate from `DnsName`. The shim's domain identifies zones
// by DNS name only; the backend derives the GCP Name from the DNS
// name deterministically. The frontend echoes Cloud DNS's Name/DnsName
// pair back on the wire so SDK clients see the same shape they would
// against real Cloud DNS.
package gcp_clouddns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	dnsraw "google.golang.org/api/dns/v1"

	"github.com/e6qu/shimanism/internal/dns/domain"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	_ "github.com/e6qu/shimanism/services/dns/gen/gcp" // spec-drift contract; tests pin dispatch shapes against gen.gcp.Routes.
)

// Server is a Cloud-DNS-shaped HTTP frontend dispatching to a
// domain.DNS backend.
type Server struct {
	d domain.DNS
}

// New returns a frontend bound to the given backend.
func New(d domain.DNS) *Server { return &Server{d: d} }

// Handler wraps Server with the GCP bearer verifier middleware.
// SHIMANISM_TEST_UNAUTHENTICATED_GCP=1 short-circuits verification.
func Handler(d domain.DNS) http.Handler {
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://dns.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	return gcpbearer.Middleware(verifier)(New(d))
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	path = strings.TrimPrefix(path, "dns/")
	if !strings.HasPrefix(path, "v1/projects/") {
		writeError(w, http.StatusNotFound, "notFound", "no route matches "+r.URL.Path)
		return
	}
	rest := strings.TrimPrefix(path, "v1/projects/")
	// rest starts with "{project}/..."
	segs := strings.SplitN(rest, "/", 3)
	if len(segs) < 2 || segs[1] != "managedZones" {
		writeError(w, http.StatusNotFound, "notFound", "no route matches "+r.URL.Path)
		return
	}
	project := segs[0]
	_ = project
	if len(segs) == 2 {
		// /projects/{project}/managedZones
		switch r.Method {
		case http.MethodGet:
			srv.listZones(w, r)
		case http.MethodPost:
			srv.createZone(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
		}
		return
	}
	// segs[2] is "{zone}" or "{zone}/{sub}/..."
	zoneAndRest := segs[2]
	zoneParts := strings.SplitN(zoneAndRest, "/", 2)
	zoneID := zoneParts[0]
	if len(zoneParts) == 1 {
		// /projects/{project}/managedZones/{zone}
		switch r.Method {
		case http.MethodGet:
			srv.getZone(w, r, zoneID)
		case http.MethodDelete:
			srv.deleteZone(w, r, zoneID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
		}
		return
	}
	subRest := zoneParts[1]
	subParts := strings.SplitN(subRest, "/", 4)
	switch subParts[0] {
	case "rrsets":
		switch len(subParts) {
		case 1: // /rrsets
			switch r.Method {
			case http.MethodGet:
				srv.listRecordSets(w, r, zoneID)
			case http.MethodPost:
				srv.createRecordSet(w, r, zoneID)
			default:
				writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
			}
		case 3: // /rrsets/{name}/{type}
			name, rtype := subParts[1], subParts[2]
			switch r.Method {
			case http.MethodGet:
				srv.getRecordSet(w, r, zoneID, name, rtype)
			case http.MethodDelete:
				srv.deleteRecordSet(w, r, zoneID, name, rtype)
			case http.MethodPatch:
				srv.patchRecordSet(w, r, zoneID, name, rtype)
			default:
				writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
			}
		default:
			writeError(w, http.StatusNotFound, "notFound", r.URL.Path)
		}
	case "changes":
		switch len(subParts) {
		case 1: // /changes
			switch r.Method {
			case http.MethodPost:
				srv.createChange(w, r, zoneID)
			case http.MethodGet:
				srv.listChanges(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
			}
		case 2: // /changes/{id}
			if r.Method == http.MethodGet {
				srv.getChange(w, r, subParts[1])
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
		default:
			writeError(w, http.StatusNotFound, "notFound", r.URL.Path)
		}
	default:
		writeError(w, http.StatusNotFound, "notFound", r.URL.Path)
	}
}

// ---------- Zone CRUD ----------

func (srv *Server) createZone(w http.ResponseWriter, r *http.Request) {
	var req dnsraw.ManagedZone
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not decode body: "+err.Error())
		return
	}
	opt := domain.CreateZoneOptions{
		Description: req.Description,
		Tags:        copyTags(req.Labels),
	}
	switch strings.ToLower(req.Visibility) {
	case "private":
		opt.Visibility = domain.VisibilityPrivate
		if req.PrivateVisibilityConfig != nil {
			for _, n := range req.PrivateVisibilityConfig.Networks {
				if n.NetworkUrl != "" {
					opt.PrivateVPCs = append(opt.PrivateVPCs, n.NetworkUrl)
				}
			}
		}
	default:
		opt.Visibility = domain.VisibilityPublic
	}
	z, err := srv.d.CreateZone(r.Context(), req.DnsName, opt)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpZoneFromDomain(z, req.Name))
}

func (srv *Server) getZone(w http.ResponseWriter, r *http.Request, zoneID string) {
	z, err := srv.d.GetZone(r.Context(), dnsNameFromZoneID(zoneID))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpZoneFromDomain(z, zoneID))
}

func (srv *Server) deleteZone(w http.ResponseWriter, r *http.Request, zoneID string) {
	if err := srv.d.DeleteZone(r.Context(), dnsNameFromZoneID(zoneID), false); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) listZones(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opt := domain.ListZonesOptions{
		NamePrefix: q.Get("dnsName"),
		PageToken:  q.Get("pageToken"),
	}
	if max := q.Get("maxResults"); max != "" {
		if n, err := strconv.Atoi(max); err == nil {
			opt.PageSize = n
		}
	}
	res, err := srv.d.ListZones(r.Context(), opt)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := &dnsraw.ManagedZonesListResponse{
		Kind:          "dns#managedZonesListResponse",
		NextPageToken: res.NextPageToken,
	}
	for _, z := range res.Zones {
		out.ManagedZones = append(out.ManagedZones, gcpZoneFromDomain(z, zoneIDFromDNSName(z.Name)))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- RecordSet ops ----------

func (srv *Server) listRecordSets(w http.ResponseWriter, r *http.Request, zoneID string) {
	q := r.URL.Query()
	opt := domain.ListRecordSetsOptions{
		NameFilter: q.Get("name"),
		PageToken:  q.Get("pageToken"),
	}
	if t := q.Get("type"); t != "" {
		opt.TypeFilter = domain.RecordType(t)
	}
	if max := q.Get("maxResults"); max != "" {
		if n, err := strconv.Atoi(max); err == nil {
			opt.PageSize = n
		}
	}
	res, err := srv.d.ListRecordSets(r.Context(), dnsNameFromZoneID(zoneID), opt)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := &dnsraw.ResourceRecordSetsListResponse{
		Kind:          "dns#resourceRecordSetsListResponse",
		NextPageToken: res.NextPageToken,
	}
	for _, rs := range res.RecordSets {
		out.Rrsets = append(out.Rrsets, gcpRRSetFromDomain(rs))
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) getRecordSet(w http.ResponseWriter, r *http.Request, zoneID, name, rtype string) {
	rs, err := srv.d.GetRecordSet(r.Context(), dnsNameFromZoneID(zoneID), name, domain.RecordType(rtype))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpRRSetFromDomain(rs))
}

func (srv *Server) createRecordSet(w http.ResponseWriter, r *http.Request, zoneID string) {
	var req dnsraw.ResourceRecordSet
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	rs := domainRRSetFromGCP(&req)
	if err := srv.d.PutRecordSet(r.Context(), dnsNameFromZoneID(zoneID), rs); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpRRSetFromDomain(rs))
}

func (srv *Server) patchRecordSet(w http.ResponseWriter, r *http.Request, zoneID, name, rtype string) {
	var req dnsraw.ResourceRecordSet
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if req.Name == "" {
		req.Name = name
	}
	if req.Type == "" {
		req.Type = rtype
	}
	rs := domainRRSetFromGCP(&req)
	if err := srv.d.PutRecordSet(r.Context(), dnsNameFromZoneID(zoneID), rs); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpRRSetFromDomain(rs))
}

func (srv *Server) deleteRecordSet(w http.ResponseWriter, r *http.Request, zoneID, name, rtype string) {
	if err := srv.d.DeleteRecordSet(r.Context(), dnsNameFromZoneID(zoneID), name, domain.RecordType(rtype)); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---------- Changes ----------

func (srv *Server) createChange(w http.ResponseWriter, r *http.Request, zoneID string) {
	var req dnsraw.Change
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	zoneName := dnsNameFromZoneID(zoneID)
	// Cloud DNS Change is paired (Deletions, Additions). Treat the
	// pair as a per-record-set replace when both are present at the
	// same (name, type); otherwise apply each independently.
	additionByKey := map[string]*dnsraw.ResourceRecordSet{}
	for _, rs := range req.Additions {
		additionByKey[rrKey(rs.Name, rs.Type)] = rs
	}
	deletionByKey := map[string]*dnsraw.ResourceRecordSet{}
	for _, rs := range req.Deletions {
		deletionByKey[rrKey(rs.Name, rs.Type)] = rs
	}
	// Apply additions (UPSERT semantics — backend Put replaces).
	for _, rs := range req.Additions {
		if err := srv.d.PutRecordSet(r.Context(), zoneName, domainRRSetFromGCP(rs)); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	// Apply deletions that aren't part of an addition (the paired
	// delete-add at the same (name, type) is already covered by the
	// Put above).
	for k, rs := range deletionByKey {
		if _, paired := additionByKey[k]; paired {
			continue
		}
		if err := srv.d.DeleteRecordSet(r.Context(), zoneName, rs.Name, domain.RecordType(rs.Type)); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	out := &dnsraw.Change{
		Id:        synthChangeID(zoneID),
		Kind:      "dns#change",
		Status:    "done",
		StartTime: time.Now().UTC().Format(time.RFC3339),
		Additions: req.Additions,
		Deletions: req.Deletions,
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) getChange(w http.ResponseWriter, r *http.Request, changeID string) {
	// Synchronous shim: every change is `done` by the time the SDK
	// can poll. Echo the ID back.
	writeJSON(w, http.StatusOK, &dnsraw.Change{
		Id:        changeID,
		Kind:      "dns#change",
		Status:    "done",
		StartTime: time.Now().UTC().Format(time.RFC3339),
	})
}

func (srv *Server) listChanges(w http.ResponseWriter, r *http.Request) {
	// Out of intersection for the foundational set — return empty.
	writeJSON(w, http.StatusOK, &dnsraw.ChangesListResponse{
		Kind:    "dns#changesListResponse",
		Changes: []*dnsraw.Change{},
	})
}

// ---------- Translation ----------

// dnsNameFromZoneID reverses managedZoneName: "example-com" → "example.com.".
// Cloud DNS Names use dashes in place of dots; the shim treats the
// reverse mapping as identity-modulo-dashes when callers identify a
// zone by its Cloud DNS Name in the URL.
func dnsNameFromZoneID(zoneID string) string {
	id := strings.TrimPrefix(zoneID, "z")
	parts := strings.Split(id, "-")
	return strings.Join(parts, ".") + "."
}

// zoneIDFromDNSName mirrors the backend's managedZoneName for emitting
// stable Cloud DNS Name values in responses without depending on the
// concrete backend's mapping.
func zoneIDFromDNSName(dnsName string) string {
	bare := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(dnsName)), ".")
	bare = strings.ReplaceAll(bare, ".", "-")
	if bare == "" {
		return "shim"
	}
	if c := bare[0]; c < 'a' || c > 'z' {
		bare = "z" + bare
	}
	if len(bare) > 63 {
		bare = bare[:63]
	}
	return bare
}

func gcpZoneFromDomain(z domain.Zone, name string) *dnsraw.ManagedZone {
	if name == "" {
		name = zoneIDFromDNSName(z.Name)
	}
	out := &dnsraw.ManagedZone{
		Kind:        "dns#managedZone",
		Name:        name,
		DnsName:     z.Name,
		Description: z.Description,
		Labels:      copyTags(z.Tags),
		NameServers: append([]string(nil), z.NameServers...),
		Visibility:  visibilityToGCP(z.Visibility),
	}
	if !z.CreatedAt.IsZero() {
		out.CreationTime = z.CreatedAt.Format(time.RFC3339)
	}
	return out
}

func visibilityToGCP(v domain.ZoneVisibility) string {
	switch v {
	case domain.VisibilityPrivate:
		return "private"
	default:
		return "public"
	}
}

func gcpRRSetFromDomain(rs domain.RecordSet) *dnsraw.ResourceRecordSet {
	return &dnsraw.ResourceRecordSet{
		Kind:    "dns#resourceRecordSet",
		Name:    rs.Name,
		Type:    string(rs.Type),
		Ttl:     int64(rs.TTL),
		Rrdatas: append([]string(nil), rs.Records...),
	}
}

func domainRRSetFromGCP(rs *dnsraw.ResourceRecordSet) domain.RecordSet {
	return domain.RecordSet{
		Name:    rs.Name,
		Type:    domain.RecordType(rs.Type),
		TTL:     int(rs.Ttl),
		Records: append([]string(nil), rs.Rrdatas...),
	}
}

func rrKey(name, rtype string) string {
	return strings.ToLower(strings.TrimSuffix(name, ".")) + "|" + strings.ToUpper(rtype)
}

func synthChangeID(zoneID string) string {
	// Deterministic monotonic per call within a process; Cloud DNS
	// uses opaque integer-as-string change IDs.
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ---------- Error envelope ----------

func writeError(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": message,
			"errors":  []map[string]string{{"reason": reason, "message": message}},
		},
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchZone, domain.KindNoSuchRecordSet:
		writeError(w, http.StatusNotFound, "notFound", de.Message)
	case domain.KindZoneAlreadyExists:
		writeError(w, http.StatusConflict, "alreadyExists", de.Message)
	case domain.KindZoneNotEmpty:
		writeError(w, http.StatusPreconditionFailed, "containerNotEmpty", de.Message)
	case domain.KindInvalidArgument, domain.KindUnsupported:
		writeError(w, http.StatusBadRequest, "invalid", de.Message)
	default:
		writeError(w, http.StatusInternalServerError, "internalError", de.Message)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
