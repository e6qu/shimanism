package gcp_artifactregistry

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	arraw "google.golang.org/api/artifactregistry/v1"

	"github.com/e6qu/shimanism/internal/registry/domain"
)

// serveControl handles the Artifact Registry v1 REST control plane
// (repositories CRUD + dockerImages + operation polling). Repository
// resources are keyed in the backend by their full AR resource name
// (projects/{p}/locations/{l}/repositories/{id}).
func (s *Server) serveControl(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/")

	// Operations poll: projects/{p}/locations/{l}/operations/{op}.
	if i := strings.Index(rest, "/operations/"); i >= 0 {
		if r.Method == http.MethodGet {
			// Every shim operation completes synchronously, so a poll
			// always reports done.
			writeJSON(w, http.StatusOK, &arraw.Operation{Name: rest, Done: true})
			return
		}
		writeARErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	idx := strings.Index(rest, "/repositories")
	if idx < 0 {
		writeARErr(w, http.StatusNotFound, "unrecognized path: "+r.URL.Path)
		return
	}
	parent := rest[:idx] // projects/{p}/locations/{l}
	tail := strings.TrimPrefix(rest[idx+len("/repositories"):], "/")

	switch {
	case tail == "" && r.Method == http.MethodPost:
		s.createRepo(w, r, parent)
	case tail == "" && r.Method == http.MethodGet:
		s.listRepos(w, r, parent)
	case tail != "" && strings.HasSuffix(tail, "/dockerImages") && r.Method == http.MethodGet:
		repoID := strings.TrimSuffix(tail, "/dockerImages")
		s.listDockerImages(w, r, parent+"/repositories/"+repoID)
	case tail != "" && r.Method == http.MethodGet:
		s.getRepo(w, r, parent+"/repositories/"+tail)
	case tail != "" && r.Method == http.MethodDelete:
		s.deleteRepo(w, r, parent+"/repositories/"+tail, parent)
	default:
		writeARErr(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
	}
}

func (s *Server) createRepo(w http.ResponseWriter, r *http.Request, parent string) {
	id := r.URL.Query().Get("repositoryId")
	if id == "" {
		writeARErr(w, http.StatusBadRequest, "repositoryId is required")
		return
	}
	var body arraw.Repository
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := parent + "/repositories/" + id
	repo, err := s.reg.CreateRepository(r.Context(), name, domain.CreateRepoOptions{Tags: body.Labels})
	if err != nil {
		writeARDomainErr(w, err)
		return
	}
	// Create is an LRO; the shim's backend create is synchronous, so the
	// operation is already done with the repository as its response.
	resp, _ := json.Marshal(repoToAR(repo, body.Format))
	writeJSON(w, http.StatusOK, &arraw.Operation{
		Name: parent + "/operations/create-" + id, Done: true, Response: resp,
	})
}

func (s *Server) getRepo(w http.ResponseWriter, r *http.Request, name string) {
	repo, err := s.reg.DescribeRepository(r.Context(), name)
	if err != nil {
		writeARDomainErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repoToAR(repo, "DOCKER"))
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request, parent string) {
	res, err := s.reg.ListRepositories(r.Context(), domain.ListOptions{})
	if err != nil {
		writeARDomainErr(w, err)
		return
	}
	out := &arraw.ListRepositoriesResponse{}
	for _, repo := range res.Repositories {
		// Only repositories under the requested parent.
		if !strings.HasPrefix(repo.Name, parent+"/repositories/") {
			continue
		}
		out.Repositories = append(out.Repositories, repoToAR(repo, "DOCKER"))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteRepo(w http.ResponseWriter, r *http.Request, name, parent string) {
	if err := s.reg.DeleteRepository(r.Context(), name, true); err != nil {
		writeARDomainErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &arraw.Operation{
		Name: parent + "/operations/delete", Done: true,
	})
}

func (s *Server) listDockerImages(w http.ResponseWriter, r *http.Request, name string) {
	res, err := s.reg.ListImages(r.Context(), name, domain.ListOptions{})
	if err != nil {
		writeARDomainErr(w, err)
		return
	}
	out := &arraw.ListDockerImagesResponse{}
	for _, img := range res.Images {
		out.DockerImages = append(out.DockerImages, &arraw.DockerImage{
			Name:           name + "/dockerImages/" + img.Digest,
			Uri:            name + "@" + img.Digest,
			Tags:           img.Tags,
			MediaType:      img.MediaType,
			ImageSizeBytes: img.Size,
			UploadTime:     img.PushedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func repoToAR(r domain.Repository, format string) *arraw.Repository {
	if format == "" {
		format = "DOCKER"
	}
	out := &arraw.Repository{Name: r.Name, Format: format, Labels: r.Tags}
	if !r.CreatedAt.IsZero() {
		out.CreateTime = r.CreatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// arError is the Google REST error envelope.
type arError struct {
	Error arErrorBody `json:"error"`
}
type arErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func writeARErr(w http.ResponseWriter, code int, msg string) {
	status := "INVALID_ARGUMENT"
	switch code {
	case http.StatusNotFound:
		status = "NOT_FOUND"
	case http.StatusConflict:
		status = "ALREADY_EXISTS"
	case http.StatusMethodNotAllowed:
		status = "FAILED_PRECONDITION"
	}
	writeJSON(w, code, arError{Error: arErrorBody{Code: code, Message: msg, Status: status}})
}

func writeARDomainErr(w http.ResponseWriter, err error) {
	switch {
	case domain.IsNotFound(err):
		writeARErr(w, http.StatusNotFound, err.Error())
	case domain.IsAlreadyExists(err):
		writeARErr(w, http.StatusConflict, err.Error())
	case domain.IsInvalidInput(err):
		writeARErr(w, http.StatusBadRequest, err.Error())
	case domain.IsNotSupported(err):
		writeARErr(w, http.StatusNotImplemented, err.Error())
	default:
		writeARErr(w, http.StatusInternalServerError, err.Error())
	}
}
