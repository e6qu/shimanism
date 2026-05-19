// Phase 4 conformance: Azure Service Bus topics REST data plane
// exercised via raw HTTP. The official azservicebus SDK speaks
// AMQP, so SDK conformance for the Azure pubsub frontend is
// deferred (same posture as the Phase 3 queue Azure cell).
package conformance_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

func TestAzureREST_PubsubFanout(t *testing.T) {
	srv := harness.StartPubsubServerAzure(t, inmem.New())
	cli := srv.URL

	mustReq := func(method, path string, body []byte, expect int) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, cli+path, bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new %s %s: %v", method, path, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		if resp.StatusCode != expect {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("%s %s: status=%d want %d body=%s", method, path, resp.StatusCode, expect, b)
		}
		return resp
	}

	// Create topic + two subscriptions.
	mustReq(http.MethodPut, "/orders", nil, http.StatusCreated).Body.Close()
	mustReq(http.MethodPut, "/orders/Subscriptions/orders-a", nil, http.StatusCreated).Body.Close()
	mustReq(http.MethodPut, "/orders/Subscriptions/orders-b", nil, http.StatusCreated).Body.Close()

	// Publish.
	mustReq(http.MethodPost, "/orders/messages", []byte("hello-fanout"), http.StatusCreated).Body.Close()

	// Each subscription should receive a copy.
	for _, sub := range []string{"orders-a", "orders-b"} {
		resp := mustReq(http.MethodPost, "/orders/Subscriptions/"+sub+"/messages/head", nil, http.StatusCreated)
		var bp struct {
			MessageId string `json:"MessageId"`
			LockToken string `json:"LockToken"`
		}
		_ = json.Unmarshal([]byte(resp.Header.Get("BrokerProperties")), &bp)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "hello-fanout" {
			t.Errorf("%s body = %q, want hello-fanout", sub, body)
		}
		// Ack via DELETE.
		mustReq(http.MethodDelete,
			"/orders/Subscriptions/"+sub+"/messages/"+bp.MessageId+"/"+bp.LockToken,
			nil, http.StatusOK).Body.Close()
	}

	// Tear down.
	mustReq(http.MethodDelete, "/orders/Subscriptions/orders-a", nil, http.StatusNoContent).Body.Close()
	mustReq(http.MethodDelete, "/orders/Subscriptions/orders-b", nil, http.StatusNoContent).Body.Close()
	mustReq(http.MethodDelete, "/orders", nil, http.StatusNoContent).Body.Close()
}
