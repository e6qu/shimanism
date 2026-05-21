package awsjson_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/awsjson"
)

// fakeHandler captures the request it received so the test can assert
// the router routed correctly without spinning up a real backend.
type fakeHandler struct{ hit bool }

func (h *fakeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.hit = true
	awsjson.WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func TestRouter_DispatchesByXAmzTarget(t *testing.T) {
	create, describe := &fakeHandler{}, &fakeHandler{}
	rt := awsjson.NewRouter("SecretsManager")
	rt.Register("CreateSecret", create)
	rt.Register("DescribeSecret", describe)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("X-Amz-Target", "SecretsManager.DescribeSecret")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Result().StatusCode)
	}
	if create.hit {
		t.Error("CreateSecret handler hit; expected DescribeSecret only")
	}
	if !describe.hit {
		t.Error("DescribeSecret handler not hit")
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-amz-json-1.1" {
		t.Errorf("Content-Type = %q, want application/x-amz-json-1.1", got)
	}
}

func TestRouter_WrongMethod(t *testing.T) {
	rt := awsjson.NewRouter("SecretsManager")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Result().StatusCode)
	}
	assertErrorType(t, w, "UnknownOperationException")
}

func TestRouter_MissingHeader(t *testing.T) {
	rt := awsjson.NewRouter("SecretsManager")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	assertErrorType(t, w, "InvalidSignatureException")
}

func TestRouter_MalformedTarget(t *testing.T) {
	rt := awsjson.NewRouter("SecretsManager")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("X-Amz-Target", "nodot")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	assertErrorType(t, w, "UnknownOperationException")
}

func TestRouter_WrongService(t *testing.T) {
	rt := awsjson.NewRouter("SecretsManager")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("X-Amz-Target", "Lambda.Invoke")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
}

func TestRouter_UnknownOperation(t *testing.T) {
	rt := awsjson.NewRouter("SecretsManager")
	rt.Register("CreateSecret", &fakeHandler{})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("X-Amz-Target", "SecretsManager.RotateSecret")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	assertErrorType(t, w, "UnknownOperationException")
}

func TestWriteBackendError_TypedError(t *testing.T) {
	w := httptest.NewRecorder()
	awsjson.WriteBackendError(w, &awsjson.BackendError{
		HTTPStatus: http.StatusNotFound,
		Type:       "ResourceNotFoundException",
		Message:    "Secrets Manager can't find the specified secret.",
	})

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Result().StatusCode)
	}
	assertErrorType(t, w, "ResourceNotFoundException")
}

func TestWriteBackendError_UntypedFallsToInternalFailure(t *testing.T) {
	w := httptest.NewRecorder()
	awsjson.WriteBackendError(w, errors.New("backend exploded"))

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Result().StatusCode)
	}
	assertErrorType(t, w, "InternalFailure")
}

func TestDecodeJSON_MalformedFails(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not-json}`))
	w := httptest.NewRecorder()

	var target struct{ Name string }
	if awsjson.DecodeJSON(w, req, &target) {
		t.Fatal("expected DecodeJSON to return false on malformed JSON")
	}
	assertErrorType(t, w, "SerializationException")
}

func TestDecodeJSON_UnknownFieldFails(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Unknown":"x"}`))
	req.ContentLength = int64(len(`{"Unknown":"x"}`))
	w := httptest.NewRecorder()

	var target struct {
		Name string `json:"Name"`
	}
	if awsjson.DecodeJSON(w, req, &target) {
		t.Fatal("expected DecodeJSON to reject unknown field")
	}
	assertErrorType(t, w, "SerializationException")
}

func assertErrorType(t *testing.T, w *httptest.ResponseRecorder, wantType string) {
	t.Helper()
	if got := w.Header().Get("X-Amzn-Errortype"); got != wantType {
		t.Errorf("X-Amzn-Errortype = %q, want %q", got, wantType)
	}
	var body struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Type != wantType {
		t.Errorf("body __type = %q, want %q", body.Type, wantType)
	}
}
