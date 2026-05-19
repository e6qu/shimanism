package aws_secretsmanager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// ----------------------------------------------------------------------
// Wire types — JSON shapes the SDK puts on / reads from the wire.
// Names match the Smithy spec exactly so the SDK accepts them.
// ----------------------------------------------------------------------

type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type createSecretRequest struct {
	Name               string  `json:"Name"`
	ClientRequestToken string  `json:"ClientRequestToken,omitempty"`
	Description        *string `json:"Description,omitempty"`
	SecretString       *string `json:"SecretString,omitempty"`
	SecretBinary       []byte  `json:"SecretBinary,omitempty"`
	Tags               []tag   `json:"Tags,omitempty"`
}

type createSecretResponse struct {
	ARN       string `json:"ARN"`
	Name      string `json:"Name"`
	VersionId string `json:"VersionId,omitempty"`
}

type getSecretValueRequest struct {
	SecretId     string `json:"SecretId"`
	VersionId    string `json:"VersionId,omitempty"`
	VersionStage string `json:"VersionStage,omitempty"`
}

type getSecretValueResponse struct {
	ARN           string   `json:"ARN"`
	Name          string   `json:"Name"`
	VersionId     string   `json:"VersionId"`
	SecretString  *string  `json:"SecretString,omitempty"`
	SecretBinary  []byte   `json:"SecretBinary,omitempty"`
	VersionStages []string `json:"VersionStages"`
	CreatedDate   float64  `json:"CreatedDate,omitempty"`
}

type putSecretValueRequest struct {
	SecretId           string  `json:"SecretId"`
	ClientRequestToken string  `json:"ClientRequestToken,omitempty"`
	SecretString       *string `json:"SecretString,omitempty"`
	SecretBinary       []byte  `json:"SecretBinary,omitempty"`
}

type putSecretValueResponse struct {
	ARN           string   `json:"ARN"`
	Name          string   `json:"Name"`
	VersionId     string   `json:"VersionId"`
	VersionStages []string `json:"VersionStages"`
}

type deleteSecretRequest struct {
	SecretId                   string `json:"SecretId"`
	ForceDeleteWithoutRecovery *bool  `json:"ForceDeleteWithoutRecovery,omitempty"`
	RecoveryWindowInDays       *int64 `json:"RecoveryWindowInDays,omitempty"`
}

type deleteSecretResponse struct {
	ARN          string  `json:"ARN"`
	Name         string  `json:"Name"`
	DeletionDate float64 `json:"DeletionDate,omitempty"`
}

type describeSecretRequest struct {
	SecretId string `json:"SecretId"`
}

type describeSecretResponse struct {
	ARN                string              `json:"ARN"`
	Name               string              `json:"Name"`
	Description        string              `json:"Description,omitempty"`
	Tags               []tag               `json:"Tags,omitempty"`
	CreatedDate        float64             `json:"CreatedDate,omitempty"`
	LastChangedDate    float64             `json:"LastChangedDate,omitempty"`
	VersionIdsToStages map[string][]string `json:"VersionIdsToStages,omitempty"`
}

type listSecretsRequest struct {
	MaxResults *int64 `json:"MaxResults,omitempty"`
	NextToken  string `json:"NextToken,omitempty"`
	// Filters not supported at this phase; documented as out of
	// intersection.
}

type secretListEntry struct {
	ARN             string  `json:"ARN"`
	Name            string  `json:"Name"`
	Description     string  `json:"Description,omitempty"`
	Tags            []tag   `json:"Tags,omitempty"`
	CreatedDate     float64 `json:"CreatedDate,omitempty"`
	LastChangedDate float64 `json:"LastChangedDate,omitempty"`
}

type listSecretsResponse struct {
	SecretList []secretListEntry `json:"SecretList"`
	NextToken  string            `json:"NextToken,omitempty"`
}

type listSecretVersionIdsRequest struct {
	SecretId          string `json:"SecretId"`
	MaxResults        *int64 `json:"MaxResults,omitempty"`
	NextToken         string `json:"NextToken,omitempty"`
	IncludeDeprecated *bool  `json:"IncludeDeprecated,omitempty"`
}

type secretVersionsListEntry struct {
	VersionId     string   `json:"VersionId"`
	VersionStages []string `json:"VersionStages"`
	CreatedDate   float64  `json:"CreatedDate,omitempty"`
}

type listSecretVersionIdsResponse struct {
	ARN       string                    `json:"ARN"`
	Name      string                    `json:"Name"`
	Versions  []secretVersionsListEntry `json:"Versions"`
	NextToken string                    `json:"NextToken,omitempty"`
}

// ----------------------------------------------------------------------
// Version-handle helpers. AWS Secrets Manager uses UUID `VersionId`
// strings; the domain uses monotonic uint64. The mapping is purely
// computational (no shim state): the monotonic number is encoded
// into the bottom 48 bits of an otherwise-zero UUID. Compat with
// real AWS UUIDs: the shim only accepts version IDs we issued, so
// the encoding is round-trip-safe.
// ----------------------------------------------------------------------

func versionIDFor(n uint64) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012x", n)
}

func versionFromID(id string) (uint64, bool) {
	// Accept the canonical zero-UUID shape we issue, AND the bare
	// hex form that some CLI invocations pass back unmodified.
	if id == "" {
		return 0, true
	}
	if i := strings.LastIndexByte(id, '-'); i >= 0 {
		id = id[i+1:]
	}
	n, err := strconv.ParseUint(id, 16, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// fakeARN constructs a shim-issued ARN. Real AWS ARNs include account
// + region + a random suffix; the shim doesn't have any of those.
// The SDK clients are tolerant of the shape as long as it starts with
// `arn:aws:secretsmanager:`.
func fakeARN(name string) string {
	return "arn:aws:secretsmanager:shim:000000000000:secret:" + name
}

// timeToEpochSeconds renders a Go time.Time as a floating-point
// epoch-seconds value, which is the AWS Secrets Manager wire format
// for `CreatedDate` / `LastChangedDate`.
func timeToEpochSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

// tagsFromMap renders a domain tag map as the AWS Smithy tag-list
// wire shape.
func tagsFromMap(m map[string]string) []tag {
	if len(m) == 0 {
		return nil
	}
	out := make([]tag, 0, len(m))
	// Deterministic order — SDK clients don't care, but conformance
	// tests assert on equality.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// keep allocation predictable; iterate in insertion-stable order
	// via sort.Strings
	sortStrings(keys)
	for _, k := range keys {
		out = append(out, tag{Key: k, Value: m[k]})
	}
	return out
}

func tagsToMap(tags []tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}
	return out
}

// sortStrings — tiny inline sort to avoid pulling in sort just for
// tag rendering. Bubble sort is fine for the tiny tag counts we see.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// pickValueBytes returns the value bytes from a CreateSecret /
// PutSecretValue request, preferring SecretString. SecretBinary is
// accepted but treated as raw bytes (the SDK base64-encodes binary
// secrets on the wire; the JSON decoder converts back to []byte).
func pickValueBytes(secretString *string, secretBinary []byte) ([]byte, bool) {
	if secretString != nil {
		return []byte(*secretString), true
	}
	if len(secretBinary) > 0 {
		out := make([]byte, len(secretBinary))
		copy(out, secretBinary)
		return out, true
	}
	return nil, false
}

// ----------------------------------------------------------------------
// Per-operation handlers
// ----------------------------------------------------------------------

func (srv *Server) createSecret(w http.ResponseWriter, r *http.Request) {
	var in createSecretRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "Name is required")
		return
	}
	opt := domain.CreateSecretOptions{
		Tags: tagsToMap(in.Tags),
	}
	if in.Description != nil {
		opt.Description = *in.Description
	}
	if v, ok := pickValueBytes(in.SecretString, in.SecretBinary); ok {
		opt.InitialValue = v
	}
	res, err := srv.s.CreateSecret(r.Context(), in.Name, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := createSecretResponse{
		ARN:  fakeARN(in.Name),
		Name: in.Name,
	}
	if res.Version > 0 {
		resp.VersionId = versionIDFor(res.Version)
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) getSecretValue(w http.ResponseWriter, r *http.Request) {
	var in getSecretValueRequest
	if !decode(w, r, &in) {
		return
	}
	if in.SecretId == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "SecretId is required")
		return
	}
	name := normaliseSecretID(in.SecretId)

	// Version selection priority: explicit VersionId > VersionStage > latest.
	// VersionStage only honours AWSCURRENT (== latest) and AWSPREVIOUS
	// (== latest-1); other stage labels are out of intersection.
	var version uint64
	if in.VersionId != "" {
		v, ok := versionFromID(in.VersionId)
		if !ok {
			writeError(w, http.StatusBadRequest, "InvalidParameterException",
				"VersionId is not a shim-issued identifier: "+in.VersionId)
			return
		}
		version = v
	} else if in.VersionStage != "" {
		switch in.VersionStage {
		case "AWSCURRENT":
			version = 0 // domain "latest"
		case "AWSPREVIOUS":
			// AWSPREVIOUS = (latest - 1). Resolve via ListVersions.
			versions, err := srv.s.ListVersions(r.Context(), name)
			if err != nil {
				mapDomainError(w, err)
				return
			}
			if len(versions) < 2 {
				writeError(w, http.StatusBadRequest, "ResourceNotFoundException",
					"no AWSPREVIOUS version for secret "+name)
				return
			}
			version = versions[len(versions)-2].Number
		default:
			writeError(w, http.StatusBadRequest, "InvalidParameterException",
				"VersionStage "+in.VersionStage+" is not supported by this shim (intersection: AWSCURRENT, AWSPREVIOUS)")
			return
		}
	}

	val, err := srv.s.GetSecretValue(r.Context(), name, version)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := getSecretValueResponse{
		ARN:           fakeARN(name),
		Name:          name,
		VersionId:     versionIDFor(val.Version),
		VersionStages: []string{"AWSCURRENT"},
		CreatedDate:   timeToEpochSeconds(val.CreatedAt),
	}
	// Always return SecretString. Binary support is deferred (see
	// OPERATIONS.md). UTF-8-invalid bytes still pass through — the
	// SDK does not validate.
	s := string(val.Value)
	resp.SecretString = &s
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) putSecretValue(w http.ResponseWriter, r *http.Request) {
	var in putSecretValueRequest
	if !decode(w, r, &in) {
		return
	}
	if in.SecretId == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "SecretId is required")
		return
	}
	name := normaliseSecretID(in.SecretId)
	value, ok := pickValueBytes(in.SecretString, in.SecretBinary)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidParameterException",
			"must provide SecretString or SecretBinary")
		return
	}
	res, err := srv.s.PutSecretValue(r.Context(), name, value)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &putSecretValueResponse{
		ARN:           fakeARN(name),
		Name:          name,
		VersionId:     versionIDFor(res.Version),
		VersionStages: []string{"AWSCURRENT"},
	})
}

func (srv *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	var in deleteSecretRequest
	if !decode(w, r, &in) {
		return
	}
	if in.SecretId == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "SecretId is required")
		return
	}
	name := normaliseSecretID(in.SecretId)
	force := false
	if in.ForceDeleteWithoutRecovery != nil {
		force = *in.ForceDeleteWithoutRecovery
	}
	if err := srv.s.DeleteSecret(r.Context(), name, force); err != nil {
		mapDomainError(w, err)
		return
	}
	resp := deleteSecretResponse{
		ARN:  fakeARN(name),
		Name: name,
	}
	if !force {
		resp.DeletionDate = timeToEpochSeconds(time.Now().UTC().Add(7 * 24 * time.Hour))
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) describeSecret(w http.ResponseWriter, r *http.Request) {
	var in describeSecretRequest
	if !decode(w, r, &in) {
		return
	}
	if in.SecretId == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "SecretId is required")
		return
	}
	name := normaliseSecretID(in.SecretId)
	s, err := srv.s.HeadSecret(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := describeSecretResponse{
		ARN:             fakeARN(name),
		Name:            name,
		Description:     s.Description,
		Tags:            tagsFromMap(s.Tags),
		CreatedDate:     timeToEpochSeconds(s.CreatedAt),
		LastChangedDate: timeToEpochSeconds(s.UpdatedAt),
	}
	if s.CurrentVersion > 0 {
		resp.VersionIdsToStages = map[string][]string{
			versionIDFor(s.CurrentVersion): {"AWSCURRENT"},
		}
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	var in listSecretsRequest
	// ListSecrets is permitted with no body in the AWS protocol — the
	// JSON decoder treats EOF as "empty".
	body, _ := io.ReadAll(r.Body)
	if len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			writeError(w, http.StatusBadRequest, "InvalidRequest",
				"malformed JSON body: "+err.Error())
			return
		}
	}
	opt := domain.ListSecretsOptions{NextToken: in.NextToken}
	if in.MaxResults != nil {
		opt.MaxResults = int(*in.MaxResults)
	}
	res, err := srv.s.ListSecrets(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := listSecretsResponse{NextToken: res.NextToken}
	for _, s := range res.Secrets {
		resp.SecretList = append(resp.SecretList, secretListEntry{
			ARN:             fakeARN(s.Name),
			Name:            s.Name,
			Description:     s.Description,
			Tags:            tagsFromMap(s.Tags),
			CreatedDate:     timeToEpochSeconds(s.CreatedAt),
			LastChangedDate: timeToEpochSeconds(s.UpdatedAt),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

// getResourcePolicy is a probe handler for the TF AWS provider's
// resource-read flow. Real AWS returns 200 with ARN + Name + a
// null ResourcePolicy when no policy is attached. The shim doesn't
// model resource policies — they're IAM-side, separate from the
// secret data plane — but TF reads this on every refresh.
type getResourcePolicyRequest struct {
	SecretId string `json:"SecretId"`
}

type getResourcePolicyResponse struct {
	ARN            string  `json:"ARN"`
	Name           string  `json:"Name"`
	ResourcePolicy *string `json:"ResourcePolicy,omitempty"`
}

func (srv *Server) getResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var in getResourcePolicyRequest
	if !decode(w, r, &in) {
		return
	}
	if in.SecretId == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "SecretId is required")
		return
	}
	name := normaliseSecretID(in.SecretId)
	// Verify the secret exists; if not, return ResourceNotFoundException.
	if _, err := srv.s.HeadSecret(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &getResourcePolicyResponse{
		ARN:  fakeARN(name),
		Name: name,
	})
}

func (srv *Server) listSecretVersionIds(w http.ResponseWriter, r *http.Request) {
	var in listSecretVersionIdsRequest
	if !decode(w, r, &in) {
		return
	}
	if in.SecretId == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "SecretId is required")
		return
	}
	name := normaliseSecretID(in.SecretId)
	versions, err := srv.s.ListVersions(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := listSecretVersionIdsResponse{
		ARN:  fakeARN(name),
		Name: name,
	}
	for i, v := range versions {
		stages := []string{}
		if i == len(versions)-1 {
			stages = []string{"AWSCURRENT"}
		} else if i == len(versions)-2 {
			stages = []string{"AWSPREVIOUS"}
		}
		resp.Versions = append(resp.Versions, secretVersionsListEntry{
			VersionId:     versionIDFor(v.Number),
			VersionStages: stages,
			CreatedDate:   timeToEpochSeconds(v.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

// normaliseSecretID accepts either a bare name or an ARN and returns
// the secret name. AWS clients are allowed to pass either; the
// domain only knows about names.
//
// Real AWS ARNs append a 6-char random suffix
// (`arn:aws:secretsmanager:<region>:<account>:secret:<name>-<6 random>`).
// Shim-issued ARNs use the fake region `shim` and append no random
// suffix. We strip the suffix only when the region segment looks
// like a real AWS region (not "shim"), avoiding the trap where a
// legitimate name like `tf-driven` reads as `tf` + suffix `driven`.
func normaliseSecretID(id string) string {
	const arnPrefix = "arn:aws:secretsmanager:"
	if !strings.HasPrefix(id, arnPrefix) {
		return id
	}
	rest := strings.TrimPrefix(id, arnPrefix)
	parts := strings.SplitN(rest, ":", 4)
	if len(parts) < 4 || parts[2] != "secret" {
		return id
	}
	region := parts[0]
	name := parts[3]
	if region == "shim" {
		// Shim-issued ARN; no random suffix to strip.
		return name
	}
	// Real-AWS ARN: strip the 6-char alnum suffix.
	if i := strings.LastIndexByte(name, '-'); i >= 0 && len(name)-i == 7 {
		ok := true
		for _, c := range name[i+1:] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				ok = false
				break
			}
		}
		if ok {
			name = name[:i]
		}
	}
	return name
}

// encodeJSON wraps json.NewEncoder(w).Encode so errors.go can call
// without re-importing json. Kept private to avoid extra surface
// area.
func encodeJSON(w io.Writer, v interface{}) error {
	return json.NewEncoder(w).Encode(v)
}
