// Conformance: GCP Firestore-shaped frontend exercised via raw
// HTTP, covering the bits the SDK can't drive — primarily the
// streaming-array shape of :runQuery responses. Real Firestore's
// REST endpoint streams `[RunQueryResponse, RunQueryResponse, ...]`
// for runQuery; the Go google.golang.org/api/firestore/v1 SDK
// decodes only a single RunQueryResponse so callers that need
// every result must read the array themselves (or use gRPC).
package conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	firestore "google.golang.org/api/firestore/v1"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

func TestGCPHTTP_Firestore_RunQueryRaw(t *testing.T) {
	srv := harness.StartNoSQLServerGCP(t, inmem.New())
	ctx := context.Background()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://firestore.googleapis.com/",
		15*time.Minute,
	)
	httpDo := func(method, path string, body any) (*http.Response, []byte) {
		var rdr io.Reader
		if body != nil {
			buf, _ := json.Marshal(body)
			rdr = bytes.NewReader(buf)
		}
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, rdr)
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}

	// Create table via the __shim_tables__ collection convention.
	tableDoc := map[string]any{
		"fields": map[string]any{
			"partitionKey": map[string]any{"stringValue": "user"},
			"sortKey":      map[string]any{"stringValue": "ts"},
			"description":  map[string]any{"stringValue": ""},
		},
	}
	parent := "/v1/projects/" + fsProject + "/databases/" + fsDatabase + "/documents"
	if resp, _ := httpDo("POST", parent+"/__shim_tables__?documentId=events", tableDoc); resp.StatusCode != 200 {
		t.Fatalf("CreateTable: %d", resp.StatusCode)
	}

	// Seed items.
	for _, e := range []struct {
		user, ts string
	}{
		{"u1", "2026-01-01"},
		{"u1", "2026-01-02"},
		{"u1", "2026-02-01"},
		{"u2", "2026-01-01"},
	} {
		// docID derived using the same algorithm as the frontend.
		docID := derivedDocID(map[string]firestore.Value{
			"user": {StringValue: e.user, ForceSendFields: []string{"StringValue"}},
			"ts":   {StringValue: e.ts, ForceSendFields: []string{"StringValue"}},
		})
		itemDoc := map[string]any{
			"fields": map[string]any{
				"user": map[string]any{"stringValue": e.user},
				"ts":   map[string]any{"stringValue": e.ts},
			},
		}
		if resp, _ := httpDo("POST", parent+"/events?documentId="+docID, itemDoc); resp.StatusCode != 200 {
			t.Fatalf("PutItem (%s/%s): %d", e.user, e.ts, resp.StatusCode)
		}
	}

	// runQuery: user == "u1".
	queryReq := map[string]any{
		"structuredQuery": map[string]any{
			"from": []map[string]any{{"collectionId": "events"}},
			"where": map[string]any{
				"fieldFilter": map[string]any{
					"field": map[string]any{"fieldPath": "user"},
					"op":    "EQUAL",
					"value": map[string]any{"stringValue": "u1"},
				},
			},
		},
	}
	resp, body := httpDo("POST", parent+":runQuery", queryReq)
	if resp.StatusCode != 200 {
		t.Fatalf("runQuery: %d\nbody: %s", resp.StatusCode, body)
	}
	var arr []firestore.RunQueryResponse
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("runQuery decode: %v\nbody: %s", err, body)
	}
	if len(arr) != 3 {
		t.Fatalf("runQuery result count = %d, want 3 (u1 has 3 items)\nbody: %s", len(arr), body)
	}
	for _, r := range arr {
		if r.Document == nil || r.Document.Fields["user"].StringValue != "u1" {
			t.Errorf("Query returned non-u1 row: %+v", r.Document)
		}
	}

	// runQuery with begins_with-style range on ts: u1 + ts in 2026-01.
	queryReq2 := map[string]any{
		"structuredQuery": map[string]any{
			"from": []map[string]any{{"collectionId": "events"}},
			"where": map[string]any{
				"compositeFilter": map[string]any{
					"op": "AND",
					"filters": []map[string]any{
						{"fieldFilter": map[string]any{
							"field": map[string]any{"fieldPath": "user"},
							"op":    "EQUAL",
							"value": map[string]any{"stringValue": "u1"},
						}},
						{"fieldFilter": map[string]any{
							"field": map[string]any{"fieldPath": "ts"},
							"op":    "GREATER_THAN_OR_EQUAL",
							"value": map[string]any{"stringValue": "2026-01"},
						}},
						{"fieldFilter": map[string]any{
							"field": map[string]any{"fieldPath": "ts"},
							"op":    "LESS_THAN",
							"value": map[string]any{"stringValue": "2026-01￿"},
						}},
					},
				},
			},
		},
	}
	resp2, body2 := httpDo("POST", parent+":runQuery", queryReq2)
	if resp2.StatusCode != 200 {
		t.Fatalf("runQuery composite: %d\nbody: %s", resp2.StatusCode, body2)
	}
	var arr2 []firestore.RunQueryResponse
	if err := json.Unmarshal(body2, &arr2); err != nil {
		t.Fatalf("runQuery composite decode: %v\nbody: %s", err, body2)
	}
	if len(arr2) != 2 {
		t.Errorf("runQuery u1+ts<2026-01 result count = %d, want 2", len(arr2))
	}
}
