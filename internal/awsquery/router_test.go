package awsquery_test

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/awsquery"
)

type fakeHandler struct{ hit bool }

func (h *fakeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.hit = true
	awsquery.WriteResult(w, "Test", nil)
}

func newPostReq(form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestRouter_DispatchesByAction(t *testing.T) {
	create, list := &fakeHandler{}, &fakeHandler{}
	rt := awsquery.NewRouter("sns")
	rt.Register("CreateTopic", create)
	rt.Register("ListTopics", list)

	req := newPostReq(url.Values{"Action": []string{"CreateTopic"}, "Name": []string{"foo"}})
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Result().StatusCode)
	}
	if list.hit {
		t.Error("ListTopics handler hit; expected CreateTopic only")
	}
	if !create.hit {
		t.Error("CreateTopic handler not hit")
	}
}

func TestRouter_RejectsWrongMethod(t *testing.T) {
	rt := awsquery.NewRouter("sns")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Result().StatusCode)
	}
}

func TestRouter_RejectsMissingAction(t *testing.T) {
	rt := awsquery.NewRouter("sns")
	req := newPostReq(url.Values{"NoAction": []string{"x"}})
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<Code>MissingAction</Code>") {
		t.Errorf("body did not include MissingAction error: %s", body)
	}
}

func TestRouter_RejectsUnknownAction(t *testing.T) {
	rt := awsquery.NewRouter("sns")
	rt.Register("CreateTopic", &fakeHandler{})
	req := newPostReq(url.Values{"Action": []string{"Unknown"}})
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	if !strings.Contains(w.Body.String(), "<Code>InvalidAction</Code>") {
		t.Errorf("expected InvalidAction code")
	}
}

type sampleResult struct {
	XMLName  xml.Name `xml:"-"`
	TopicArn string   `xml:"TopicArn"`
}

func TestWriteResult_WrapsInOpResponseEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	awsquery.WriteResult(w, "CreateTopic", &sampleResult{TopicArn: "arn:aws:sns:test"})

	body := w.Body.String()
	wants := []string{
		"<CreateTopicResponse>",
		"<CreateTopicResult>",
		"<TopicArn>arn:aws:sns:test</TopicArn>",
		"</CreateTopicResult>",
		"<ResponseMetadata>",
		"<RequestId>",
		"</ResponseMetadata>",
		"</CreateTopicResponse>",
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody: %s", want, body)
		}
	}
}

func TestWriteBackendError_TypedError(t *testing.T) {
	w := httptest.NewRecorder()
	awsquery.WriteBackendError(w, &awsquery.BackendError{
		HTTPStatus: http.StatusNotFound,
		Type:       "Sender",
		Code:       "NotFound",
		Message:    "topic does not exist",
	})

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Result().StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<Code>NotFound</Code>") {
		t.Errorf("body missing <Code>NotFound</Code>: %s", body)
	}
}

func TestWriteBackendError_UntypedFallsToInternalFailure(t *testing.T) {
	w := httptest.NewRecorder()
	awsquery.WriteBackendError(w, errors.New("backend exploded"))

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Result().StatusCode)
	}
	if !strings.Contains(w.Body.String(), "<Code>InternalFailure</Code>") {
		t.Errorf("expected InternalFailure code")
	}
}
