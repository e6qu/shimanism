// Package aws_lambda is the AWS Lambda-shaped HTTP frontend.
// Lambda uses the restJson1 wire protocol — actual REST routes
// with JSON request + response bodies, not awsQuery/awsJson*.
package aws_lambda

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type Server struct {
	s domain.Functions
}

func New(s domain.Functions) *Server { return &Server{s: s} }

var (
	reFunctions      = regexp.MustCompile(`^/2015-03-31/functions/?$`)
	reFunction       = regexp.MustCompile(`^/2015-03-31/functions/([^/]+)$`)
	reFunctionConfig = regexp.MustCompile(`^/2015-03-31/functions/([^/]+)/configuration$`)
	reFunctionCode   = regexp.MustCompile(`^/2015-03-31/functions/([^/]+)/code$`)
	reFunctionVers   = regexp.MustCompile(`^/2015-03-31/functions/([^/]+)/versions$`)
	reFunctionURL    = regexp.MustCompile(`^/2021-10-31/functions/([^/]+)/url$`)
	reFunctionTags   = regexp.MustCompile(`^/2017-03-31/tags/([^/]+)$`)
	reFunctionPolicy = regexp.MustCompile(`^/2015-03-31/functions/([^/]+)/policy$`)
	reFunctionCC     = regexp.MustCompile(`^/2017-10-31/functions/([^/]+)/concurrency$`)
	reFunctionECfg   = regexp.MustCompile(`^/2016-09-25/functions/([^/]+)/event-invoke-config$`)
	reCodeSign       = regexp.MustCompile(`^/2020-06-30/functions/([^/]+)/code-signing-config$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reFunctionConfig.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getFunctionConfiguration(w, r, m[1])
		case http.MethodPut:
			srv.updateFunctionConfiguration(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "InvalidRequest", method+" not allowed on configuration")
		}
		return
	}
	if m := reFunctionCode.FindStringSubmatch(path); m != nil && method == http.MethodPut {
		srv.updateFunctionCode(w, r, m[1])
		return
	}
	// Importer Read-path subresources. Each is a category-2 endpoint
	// — feature unset on a plain function — returning the source
	// cloud's actual canonical-empty envelope so the provider doesn't
	// crash and doesn't propose phantom diffs.
	if m := reFunctionVers.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.listVersions(w, r, m[1])
		return
	}
	if m := reFunctionURL.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.getFunctionURLConfig(w, r, m[1])
		return
	}
	if m := reFunctionTags.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.listTags(w, r, m[1])
		return
	}
	if m := reFunctionPolicy.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.getPolicy(w, r, m[1])
		return
	}
	if m := reFunctionCC.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.getConcurrency(w, r, m[1])
		return
	}
	if m := reFunctionECfg.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.getEventInvokeConfig(w, r, m[1])
		return
	}
	if m := reCodeSign.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.getCodeSigningConfig(w, r, m[1])
		return
	}
	if m := reFunction.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getFunction(w, r, m[1])
		case http.MethodDelete:
			srv.deleteFunction(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "InvalidRequest", method+" not allowed on function")
		}
		return
	}
	if reFunctions.MatchString(path) {
		switch method {
		case http.MethodPost:
			srv.createFunction(w, r)
			return
		case http.MethodGet:
			srv.listFunctions(w, r)
			return
		}
	}

	writeError(w, http.StatusNotFound, "ResourceNotFoundException",
		"no Lambda route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Wire shapes (subset of the Lambda restJson1 schema)
// ----------------------------------------------------------------------

type createFunctionRequest struct {
	FunctionName string             `json:"FunctionName"`
	Code         *functionCode      `json:"Code,omitempty"`
	PackageType  string             `json:"PackageType,omitempty"`
	MemorySize   int                `json:"MemorySize,omitempty"`
	Timeout      int                `json:"Timeout,omitempty"`
	Environment  *environmentConfig `json:"Environment,omitempty"`
}

type functionCode struct {
	ImageUri string `json:"ImageUri,omitempty"`
}

type environmentConfig struct {
	Variables map[string]string `json:"Variables,omitempty"`
}

type updateFunctionConfigurationRequest struct {
	MemorySize  int                `json:"MemorySize,omitempty"`
	Timeout     int                `json:"Timeout,omitempty"`
	Environment *environmentConfig `json:"Environment,omitempty"`
}

type updateFunctionCodeRequest struct {
	ImageUri string `json:"ImageUri,omitempty"`
}

type functionConfiguration struct {
	FunctionName     string                `json:"FunctionName"`
	FunctionArn      string                `json:"FunctionArn"`
	PackageType      string                `json:"PackageType"`
	State            string                `json:"State"`
	LastUpdateStatus string                `json:"LastUpdateStatus,omitempty"`
	MemorySize       int                   `json:"MemorySize,omitempty"`
	Timeout          int                   `json:"Timeout,omitempty"`
	Environment      *environmentResponse  `json:"Environment,omitempty"`
	Code             *functionCodeLocation `json:"Code,omitempty"`
	CodeSha256       string                `json:"CodeSha256,omitempty"`
	LastModified     string                `json:"LastModified,omitempty"`
	Version          string                `json:"Version,omitempty"`
}

type environmentResponse struct {
	Variables map[string]string `json:"Variables,omitempty"`
}

type functionCodeLocation struct {
	ImageUri string `json:"ImageUri,omitempty"`
}

type listFunctionsResponse struct {
	Functions []*functionConfiguration `json:"Functions"`
}

// ----------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------

func (srv *Server) createFunction(w http.ResponseWriter, r *http.Request) {
	var body createFunctionRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.FunctionName == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", "FunctionName is required")
		return
	}
	if body.PackageType != "" && body.PackageType != "Image" {
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException",
			"PackageType=Zip is out of intersection; only Image is supported")
		return
	}
	if body.Code == nil || body.Code.ImageUri == "" {
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", "Code.ImageUri is required")
		return
	}
	opt := domain.CreateFunctionOptions{
		Image:          body.Code.ImageUri,
		MemoryBytes:    int64(body.MemorySize) * 1024 * 1024,
		TimeoutSeconds: body.Timeout,
	}
	if body.Environment != nil {
		opt.Environment = body.Environment.Variables
	}
	fn, err := srv.s.CreateFunction(r.Context(), body.FunctionName, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, functionToAWS(fn))
}

func (srv *Server) getFunction(w http.ResponseWriter, r *http.Request, name string) {
	fn, err := srv.s.DescribeFunction(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	// `GetFunction` returns a wrapper with Configuration + Code; the
	// shim emits the same envelope.
	type getFunctionResponse struct {
		Configuration *functionConfiguration `json:"Configuration"`
		Code          *functionCodeLocation  `json:"Code,omitempty"`
	}
	cfg := functionToAWS(fn)
	writeJSON(w, http.StatusOK, &getFunctionResponse{
		Configuration: cfg,
		Code:          cfg.Code,
	})
}

func (srv *Server) getFunctionConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	fn, err := srv.s.DescribeFunction(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, functionToAWS(fn))
}

func (srv *Server) deleteFunction(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteFunction(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) listFunctions(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListFunctions(r.Context(), domain.ListFunctionsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := listFunctionsResponse{}
	for _, fn := range res.Functions {
		out.Functions = append(out.Functions, functionToAWS(fn))
	}
	writeJSON(w, http.StatusOK, &out)
}

func (srv *Server) updateFunctionConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	var body updateFunctionConfigurationRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.UpdateFunctionOptions{
		MemoryBytes:    int64(body.MemorySize) * 1024 * 1024,
		TimeoutSeconds: body.Timeout,
	}
	if body.Environment != nil {
		opt.Environment = body.Environment.Variables
	}
	if err := srv.s.UpdateFunction(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	fn, _ := srv.s.DescribeFunction(r.Context(), name)
	writeJSON(w, http.StatusOK, functionToAWS(fn))
}

func (srv *Server) updateFunctionCode(w http.ResponseWriter, r *http.Request, name string) {
	var body updateFunctionCodeRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := srv.s.UpdateFunction(r.Context(), name, domain.UpdateFunctionOptions{
		Image: body.ImageUri,
	}); err != nil {
		mapDomainError(w, err)
		return
	}
	fn, _ := srv.s.DescribeFunction(r.Context(), name)
	writeJSON(w, http.StatusOK, functionToAWS(fn))
}

// ----------------------------------------------------------------------
// Importer Read-path subresource handlers
//
// Each returns the source cloud's canonical-unset envelope for a
// feature that's out of the Phase 7 intersection. Real Lambda
// always returns at least the $LATEST version + empty tag/policy
// envelopes for a freshly-created function. Without these the
// hashicorp/aws aws_lambda_function importer crashes mid-Read.
// ----------------------------------------------------------------------

func (srv *Server) listVersions(w http.ResponseWriter, r *http.Request, name string) {
	fn, err := srv.s.DescribeFunction(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	cfg := functionToAWS(fn)
	cfg.Version = "$LATEST"
	writeJSON(w, http.StatusOK, map[string]any{"Versions": []*functionConfiguration{cfg}})
}

func (srv *Server) getFunctionURLConfig(w http.ResponseWriter, r *http.Request, name string) {
	// No URL config configured on a plain function.
	_ = name
	writeError(w, http.StatusNotFound, "ResourceNotFoundException",
		"function URL config does not exist")
}

func (srv *Server) listTags(w http.ResponseWriter, r *http.Request, name string) {
	_ = name
	if _, err := srv.s.DescribeFunction(r.Context(), nameFromARN(name)); err != nil {
		// continue — Lambda's ListTags is tolerant
	}
	writeJSON(w, http.StatusOK, map[string]map[string]string{"Tags": {}})
}

func (srv *Server) getPolicy(w http.ResponseWriter, r *http.Request, name string) {
	_ = name
	writeError(w, http.StatusNotFound, "ResourceNotFoundException",
		"resource policy does not exist for this function")
}

func (srv *Server) getConcurrency(w http.ResponseWriter, r *http.Request, name string) {
	_ = name
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (srv *Server) getEventInvokeConfig(w http.ResponseWriter, r *http.Request, name string) {
	_ = name
	writeError(w, http.StatusNotFound, "ResourceNotFoundException",
		"event invoke config does not exist")
}

func (srv *Server) getCodeSigningConfig(w http.ResponseWriter, r *http.Request, name string) {
	_ = name
	writeJSON(w, http.StatusOK, map[string]any{})
}

// nameFromARN extracts the function name from an ARN-or-name.
func nameFromARN(s string) string {
	if i := strings.LastIndex(s, ":"); i >= 0 && strings.HasPrefix(s, "arn:") {
		return s[i+1:]
	}
	return s
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func awsStateFromDomain(s domain.Status) string {
	switch s {
	case domain.StatusCreating:
		return "Pending"
	case domain.StatusAvailable:
		return "Active"
	case domain.StatusUpdating:
		return "Active"
	case domain.StatusDeleting:
		return "Inactive"
	default:
		return "Failed"
	}
}

func awsLastUpdateStatus(s domain.Status) string {
	switch s {
	case domain.StatusUpdating:
		return "InProgress"
	case domain.StatusAvailable:
		return "Successful"
	default:
		return ""
	}
}

func functionToAWS(fn domain.Function) *functionConfiguration {
	out := &functionConfiguration{
		FunctionName:     fn.Name,
		FunctionArn:      "arn:aws:lambda:us-east-1:000000000000:function:" + fn.Name,
		PackageType:      "Image",
		State:            awsStateFromDomain(fn.Status),
		LastUpdateStatus: awsLastUpdateStatus(fn.Status),
		MemorySize:       int(fn.MemoryBytes / (1024 * 1024)),
		Timeout:          fn.TimeoutSeconds,
		Code:             &functionCodeLocation{ImageUri: fn.Image},
		LastModified:     time.Now().UTC().Format(time.RFC3339),
	}
	if len(fn.Environment) > 0 {
		out.Environment = &environmentResponse{Variables: fn.Environment}
	}
	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContentException", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContentException", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
