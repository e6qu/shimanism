package ec2query_test

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/ec2query"
)

type fakeHandler struct{ hit bool }

func (h *fakeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.hit = true
	ec2query.WriteResult(w, "Test", nil)
}

func newPostReq(form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestRouter_DispatchesByAction(t *testing.T) {
	describe, run := &fakeHandler{}, &fakeHandler{}
	rt := ec2query.NewRouter("ec2")
	rt.Register("DescribeInstances", describe)
	rt.Register("RunInstances", run)

	req := newPostReq(url.Values{"Action": []string{"DescribeInstances"}})
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Result().StatusCode)
	}
	if run.hit {
		t.Error("RunInstances handler hit; expected DescribeInstances only")
	}
	if !describe.hit {
		t.Error("DescribeInstances handler not hit")
	}
}

func TestRouter_RejectsWrongMethod(t *testing.T) {
	rt := ec2query.NewRouter("ec2")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Result().StatusCode)
	}
}

func TestRouter_RejectsMissingAction(t *testing.T) {
	rt := ec2query.NewRouter("ec2")
	req := newPostReq(url.Values{"NoAction": []string{"x"}})
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	if !strings.Contains(w.Body.String(), "<Code>MissingAction</Code>") {
		t.Errorf("body did not include MissingAction code: %s", w.Body.String())
	}
}

func TestRouter_RejectsUnknownAction(t *testing.T) {
	rt := ec2query.NewRouter("ec2")
	rt.Register("DescribeVpcs", &fakeHandler{})
	req := newPostReq(url.Values{"Action": []string{"UnknownOp"}})
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	if !strings.Contains(w.Body.String(), "<Code>InvalidAction</Code>") {
		t.Errorf("expected InvalidAction code: %s", w.Body.String())
	}
}

type instanceSet struct {
	XMLName       xml.Name `xml:"-"`
	ReservationID string   `xml:"reservationId"`
}

func TestWriteResult_EC2Envelope(t *testing.T) {
	w := httptest.NewRecorder()
	ec2query.WriteResult(w, "DescribeInstances", &instanceSet{ReservationID: "r-abc123"})

	body := w.Body.String()
	wants := []string{
		`<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">`,
		`<requestId>`,
		`<reservationId>r-abc123</reservationId>`,
		`</DescribeInstancesResponse>`,
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody: %s", want, body)
		}
	}
	// EC2 success responses do NOT have an <OpNameResult> wrapper.
	if strings.Contains(body, "Result>") {
		t.Errorf("EC2 response must not contain an OpNameResult wrapper: %s", body)
	}
}

func TestWriteResult_NoDoubleWrapper(t *testing.T) {
	w := httptest.NewRecorder()
	ec2query.WriteResult(w, "CreateVpc", nil)

	body := w.Body.String()
	if strings.Contains(body, "Result>") {
		t.Errorf("EC2 response must not contain OpNameResult wrapper: %s", body)
	}
	if !strings.Contains(body, "<CreateVpcResponse") {
		t.Errorf("missing CreateVpcResponse element: %s", body)
	}
}

func TestWriteError_EC2Envelope(t *testing.T) {
	w := httptest.NewRecorder()
	ec2query.WriteError(w, http.StatusBadRequest,
		"InvalidVpcID.NotFound", "The vpc ID 'vpc-xxx' does not exist")

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Result().StatusCode)
	}
	body := w.Body.String()
	wants := []string{
		"<Response>",
		"<Errors>",
		"<Error>",
		"<Code>InvalidVpcID.NotFound</Code>",
		"<Message>The vpc ID",
		"</Error>",
		"</Errors>",
		"<RequestID>",
		"</Response>",
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("error body missing %q\nbody: %s", want, body)
		}
	}
	// EC2 errors must NOT contain awsQuery's <Type> field.
	if strings.Contains(body, "<Type>") {
		t.Errorf("EC2 error envelope must not contain <Type> element: %s", body)
	}
	// EC2 errors must NOT use awsQuery's <ErrorResponse> wrapper.
	if strings.Contains(body, "ErrorResponse") {
		t.Errorf("EC2 error envelope must not use <ErrorResponse>: %s", body)
	}
}

func TestWriteBackendError_TypedError(t *testing.T) {
	w := httptest.NewRecorder()
	ec2query.WriteBackendError(w, &ec2query.BackendError{
		HTTPStatus: http.StatusNotFound,
		Code:       "InvalidInstanceID.NotFound",
		Message:    "instance does not exist",
	})

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Result().StatusCode)
	}
	if !strings.Contains(w.Body.String(), "<Code>InvalidInstanceID.NotFound</Code>") {
		t.Errorf("missing error code: %s", w.Body.String())
	}
}

func TestWriteBackendError_UntypedFallsToInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	ec2query.WriteBackendError(w, errors.New("backend exploded"))

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Result().StatusCode)
	}
	if !strings.Contains(w.Body.String(), "<Code>InternalError</Code>") {
		t.Errorf("expected InternalError code: %s", w.Body.String())
	}
}
