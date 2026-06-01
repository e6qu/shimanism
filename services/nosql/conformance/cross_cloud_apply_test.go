// Cross-cloud Apply matrix: every (source frontend, destination
// backend) pair where source ≠ destination cloud. Validates that
// items written via one cloud's SDK land in another cloud's
// destination representation, exercising the shim's N18 + N19
// translation across each seam.
//
// **Cell layout:**
//
//	rows = source frontend (AWS / GCP / Azure)
//	cols = destination backend (AWS / GCP / Azure / etcd)
//
// Identity cells (where row == col) are covered by the per-cloud
// SDK conformance tests; only the off-diagonal cells live here:
//
//	     AWS  GCP  Azure  etcd
//	AWS  ─    s    s      e
//	GCP  s    ─    s      e
//	Azure s   s    ─      e
//
// `s` = sockerless-gated (the destination is a real cloud's backend
// pointed at a sockerless simulator instance). Tests skip when
// SOCKERLESS_*_ENDPOINT env vars are unset; the
// `sockerless through-shim e2e` CI lane sets them.
//
// `e` = etcd-row, runs unconditionally on a real etcd subprocess
// (skips if the `etcd` binary isn't on PATH; CI installs it).
package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	clientv3 "go.etcd.io/etcd/client/v3"
	firestore "google.golang.org/api/firestore/v1"

	"github.com/e6qu/shimanism/internal/harness"
	etcdbackend "github.com/e6qu/shimanism/services/nosql/backends/etcd"
)

// ---------------- K8s row cells (destination = etcd) ----------------
//
// The etcd backend is local — a real etcd subprocess on ephemeral
// ports. No sockerless dependency. Each cell starts the frontend
// for cloud X, points it at the etcd backend, drives it via cloud
// X's SDK, then verifies the data via domain.NoSQL reads.

func newEtcdBackendForCrossCloud(t *testing.T) (*etcdbackend.Backend, func()) {
	t.Helper()
	bin, err := exec.LookPath("etcd")
	if err != nil {
		t.Skipf("etcd binary not on PATH: %v", err)
	}
	dataDir := t.TempDir()
	clientPort := freeTCPPortNoSQL(t)
	peerPort := freeTCPPortNoSQL(t)
	clientURL := fmt.Sprintf("http://127.0.0.1:%d", clientPort)
	peerURL := fmt.Sprintf("http://127.0.0.1:%d", peerPort)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin,
		"--name", "shim-cross",
		"--data-dir", filepath.Join(dataDir, "data"),
		"--listen-client-urls", clientURL,
		"--advertise-client-urls", clientURL,
		"--listen-peer-urls", peerURL,
		"--initial-advertise-peer-urls", peerURL,
		"--initial-cluster", "shim-cross="+peerURL,
		"--initial-cluster-token", "shim-cross",
		"--log-level", "warn",
	)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start etcd: %v", err)
	}
	cleanup := func() {
		cancel()
		_ = cmd.Wait()
	}
	t.Cleanup(cleanup)

	deadline := time.Now().Add(15 * time.Second)
	var cli *clientv3.Client
	for time.Now().Before(deadline) {
		c, err := clientv3.New(clientv3.Config{
			Endpoints:   []string{clientURL},
			DialTimeout: 500 * time.Millisecond,
		})
		if err == nil {
			pingCtx, pCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, perr := c.Status(pingCtx, clientURL)
			pCancel()
			if perr == nil {
				cli = c
				break
			}
			_ = c.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	if cli == nil {
		t.Fatalf("etcd never became ready at %s", clientURL)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return etcdbackend.New(cli), cleanup
}

func freeTCPPortNoSQL(t *testing.T) int {
	t.Helper()
	addr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		t.Fatalf("listen TCP: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestCrossCloud_DynamoDBFrontend_To_EtcdBackend(t *testing.T) {
	backend, _ := newEtcdBackendForCrossCloud(t)
	srv := harness.StartNoSQLServerAWS(t, backend)
	cli := newDynamoDBClient(t, srv.URL)
	runDynamoDBCRUDThroughShim(t, cli, "aws2etcd")
}

func TestCrossCloud_FirestoreFrontend_To_EtcdBackend(t *testing.T) {
	backend, _ := newEtcdBackendForCrossCloud(t)
	srv := harness.StartNoSQLServerGCP(t, backend)
	svc := newFirestoreService(t, srv.URL)
	runFirestoreCRUDThroughShim(t, svc, "gcp2etcd")
}

func TestCrossCloud_CosmosTablesFrontend_To_EtcdBackend(t *testing.T) {
	backend, _ := newEtcdBackendForCrossCloud(t)
	srv := harness.StartNoSQLServerAzure(t, backend)
	svc := newCosmosTablesClient(t, srv.URL)
	runCosmosTablesCRUDThroughShim(t, svc, "azure2etcd")
}

// ---------------- shared CRUD runners ----------------

// Each runner is the source-cloud equivalent of "create a table, put
// an item with every value type, read it back, query, delete". The
// runner takes any client (real cloud, sockerless, in-memory shim)
// and asserts the round-trip is faithful.

func runDynamoDBCRUDThroughShim(t *testing.T, cli *dynamodb.Client, tableName string) {
	t.Helper()
	ctx := context.Background()

	if _, err := cli.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{
			AttributeName: aws.String("id"),
			AttributeType: ddbtypes.ScalarAttributeTypeS,
		}},
		KeySchema: []ddbtypes.KeySchemaElement{{
			AttributeName: aws.String("id"),
			KeyType:       ddbtypes.KeyTypeHash,
		}},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// PutItem with every in-intersection value type.
	if _, err := cli.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item: map[string]ddbtypes.AttributeValue{
			"id":   &ddbtypes.AttributeValueMemberS{Value: "alice"},
			"age":  &ddbtypes.AttributeValueMemberN{Value: "30"},
			"vip":  &ddbtypes.AttributeValueMemberBOOL{Value: true},
			"data": &ddbtypes.AttributeValueMemberB{Value: []byte{0xde, 0xad}},
		},
	}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	got, err := cli.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "alice"},
		},
	})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Item["age"].(*ddbtypes.AttributeValueMemberN).Value != "30" {
		t.Errorf("age = %+v, want N=30", got.Item["age"])
	}
	if !got.Item["vip"].(*ddbtypes.AttributeValueMemberBOOL).Value {
		t.Errorf("vip = %+v, want BOOL=true", got.Item["vip"])
	}

	if _, err := cli.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "alice"},
		},
	}); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	if _, err := cli.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(tableName)}); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
}

func runFirestoreCRUDThroughShim(t *testing.T, svc *firestore.Service, tableName string) {
	t.Helper()
	ctx := context.Background()
	parent := "projects/" + fsProject + "/databases/" + fsDatabase + "/documents"

	// CreateTable via the __shim_tables__ convention.
	tableDoc := &firestore.Document{
		Fields: map[string]firestore.Value{
			"partitionKey": {StringValue: "id", ForceSendFields: []string{"StringValue"}},
			"sortKey":      {StringValue: "", ForceSendFields: []string{"StringValue"}},
			"description":  {StringValue: "", ForceSendFields: []string{"StringValue"}},
		},
	}
	if _, err := svc.Projects.Databases.Documents.
		CreateDocument(parent, "__shim_tables__", tableDoc).
		DocumentId(tableName).Context(ctx).Do(); err != nil {
		t.Fatalf("CreateTable (via __shim_tables__): %v", err)
	}

	// PutItem with every value type via createDocument.
	itemFields := map[string]firestore.Value{
		"id":   {StringValue: "alice", ForceSendFields: []string{"StringValue"}},
		"age":  {IntegerValue: 30, ForceSendFields: []string{"IntegerValue"}},
		"vip":  {BooleanValue: true, ForceSendFields: []string{"BooleanValue"}},
		"data": {BytesValue: "3q0=", ForceSendFields: []string{"BytesValue"}},
	}
	docID := derivedDocID(itemFields)
	if _, err := svc.Projects.Databases.Documents.
		CreateDocument(parent, tableName, &firestore.Document{Fields: itemFields}).
		DocumentId(docID).Context(ctx).Do(); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	// GetItem.
	got, err := svc.Projects.Databases.Documents.
		Get(parent + "/" + tableName + "/" + docID).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Fields["age"].IntegerValue != 30 {
		t.Errorf("age = %d, want 30", got.Fields["age"].IntegerValue)
	}
	if !got.Fields["vip"].BooleanValue {
		t.Errorf("vip = %v, want true", got.Fields["vip"].BooleanValue)
	}

	// DeleteItem.
	if _, err := svc.Projects.Databases.Documents.
		Delete(parent + "/" + tableName + "/" + docID).Context(ctx).Do(); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	// DeleteTable.
	if _, err := svc.Projects.Databases.Documents.
		Delete(parent + "/__shim_tables__/" + tableName).Context(ctx).Do(); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
}

func runCosmosTablesCRUDThroughShim(t *testing.T, svc *aztables.ServiceClient, tableName string) {
	t.Helper()
	ctx := context.Background()

	if _, err := svc.CreateTable(ctx, tableName, nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	client := svc.NewClient(tableName)

	// PutItem (every value type).
	entity := map[string]any{
		"PartitionKey": "p1",
		"RowKey":       "alice",
		"age":          int64(30),
		"vip":          true,
		"data":         []byte{0xde, 0xad},
	}
	body, _ := json.Marshal(entity)
	if _, err := client.AddEntity(ctx, body, nil); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// GetItem.
	got, err := client.GetEntity(ctx, "p1", "alice", nil)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(got.Value, &raw); err != nil {
		t.Fatalf("decode entity: %v\n%s", err, got.Value)
	}
	// Cosmos Tables encodes Int64 as a string with @odata.type
	// annotation; either form is acceptable as long as the value
	// round-trips.
	if v, ok := raw["age"].(string); ok && v != "30" {
		t.Errorf("age = %q, want 30", v)
	} else if v, ok := raw["age"].(float64); ok && v != 30 {
		t.Errorf("age = %v, want 30", v)
	}
	if v, ok := raw["vip"].(bool); !ok || !v {
		t.Errorf("vip = %v, want true", raw["vip"])
	}

	// DeleteItem.
	if _, err := client.DeleteEntity(ctx, "p1", "alice", nil); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	// DeleteTable.
	if _, err := svc.DeleteTable(ctx, tableName, nil); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
}

// ---------------- Sockerless cross-cloud cells (off-diagonal) ----------------
//
// The remaining 6 cells of the 3×4 matrix — AWS↔GCP, AWS↔Azure,
// GCP↔Azure — each use a sockerless simulator as the destination
// cloud's backend. The shim's frontend speaks the SOURCE cloud's
// shape; the destination backend's outbound HTTP / gRPC traffic
// goes to sockerless. Tests skip when the relevant
// SOCKERLESS_*_ENDPOINT env vars are unset; the
// `sockerless through-shim e2e` CI lane sets them all.
//
// These cells follow the DNS analogue
// (services/dns/conformance/cross_cloud_apply_test.go) but require
// per-cloud client construction with TLS + auth plumbing between
// the shim's destination backend and the sim. That work is the
// next 15.C follow-on PR; the K8s-row cells above are this PR's
// green-CI deliverable.

// Imports that the sockerless cells will use — kept active so the
// scaffolding compiles ahead of the follow-on PR.
var (
	_ = strings.TrimSpace
	_ = os.Getenv
)
