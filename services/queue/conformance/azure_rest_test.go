// Phase 3 conformance: Azure Service Bus REST data-plane exercised
// via raw HTTP. The official azservicebus SDK drives Service Bus
// over AMQP, not REST, so SDK conformance for the Azure frontend
// is documented as deferred in PLAN.md. Until the AMQP fidelity
// tier lands, raw-HTTP exercise is the contract.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

func TestAzureREST_QueueLifecycle(t *testing.T) {
	srv := harness.StartQueueServerAzure(t, inmem.New())
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

	// Create queue.
	mustReq(http.MethodPut, "/orders", nil, http.StatusCreated).Body.Close()

	// Send a message.
	mustReq(http.MethodPost, "/orders/messages", []byte("hello-azure"), http.StatusCreated).Body.Close()

	// Peek-and-lock.
	resp := mustReq(http.MethodPost, "/orders/messages/head", nil, http.StatusCreated)
	defer resp.Body.Close()
	bp := resp.Header.Get("BrokerProperties")
	if bp == "" {
		t.Fatal("BrokerProperties header missing")
	}
	var bpJSON struct {
		MessageId string `json:"MessageId"`
		LockToken string `json:"LockToken"`
	}
	if err := json.Unmarshal([]byte(bp), &bpJSON); err != nil {
		t.Fatalf("BrokerProperties decode: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello-azure" {
		t.Errorf("body = %q, want hello-azure", body)
	}

	// Renew lock. The Azure REST receipt is encoded as messageID|lockToken
	// internally; the URL path is the natural pair.
	mustReq(http.MethodPost,
		"/orders/messages/"+bpJSON.MessageId+"/"+bpJSON.LockToken,
		nil, http.StatusOK).Body.Close()

	// Complete (delete).
	mustReq(http.MethodDelete,
		"/orders/messages/"+bpJSON.MessageId+"/"+bpJSON.LockToken,
		nil, http.StatusOK).Body.Close()

	// List queues.
	resp = mustReq(http.MethodGet, "/$Resources/Queues", nil, http.StatusOK)
	var list struct {
		Queues []string `json:"queues"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Queues) != 1 || list.Queues[0] != "orders" {
		t.Errorf("list = %+v, want [orders]", list.Queues)
	}

	// Delete queue.
	mustReq(http.MethodDelete, "/orders", nil, http.StatusNoContent).Body.Close()
}
