// Conformance: AWS DynamoDB-shaped frontend exercised by the official
// aws-sdk-go-v2/service/dynamodb SDK. The SDK is pointed at the shim
// via BaseEndpoint; the shim's SigV4 verifier checks the request
// signature against the trusted test credentials.
package conformance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

func newDynamoDBClient(t *testing.T, endpoint string) *dynamodb.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestAWSSDK_DynamoDB_TableLifecycle(t *testing.T) {
	srv := harness.StartNoSQLServerAWS(t, inmem.New())
	cli := newDynamoDBClient(t, srv.URL)
	ctx := context.Background()

	// CreateTable — partition-key-only schema.
	create, err := cli.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("users"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{
			AttributeName: aws.String("id"),
			AttributeType: ddbtypes.ScalarAttributeTypeS,
		}},
		KeySchema: []ddbtypes.KeySchemaElement{{
			AttributeName: aws.String("id"),
			KeyType:       ddbtypes.KeyTypeHash,
		}},
		BillingMode: ddbtypes.BillingModePayPerRequest,
		Tags: []ddbtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("test")},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if create.TableDescription == nil || aws.ToString(create.TableDescription.TableName) != "users" {
		t.Fatalf("CreateTable returned bad table: %+v", create.TableDescription)
	}

	// Duplicate CreateTable should fail with ResourceInUseException.
	_, err = cli.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("users"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{
			AttributeName: aws.String("id"),
			AttributeType: ddbtypes.ScalarAttributeTypeS,
		}},
		KeySchema: []ddbtypes.KeySchemaElement{{
			AttributeName: aws.String("id"),
			KeyType:       ddbtypes.KeyTypeHash,
		}},
	})
	if err == nil {
		t.Fatal("CreateTable duplicate: want ResourceInUseException, got nil")
	}
	var inUse *ddbtypes.ResourceInUseException
	if !errors.As(err, &inUse) {
		t.Errorf("CreateTable duplicate: want ResourceInUseException, got %T (%v)", err, err)
	}

	// DescribeTable round-trip.
	desc, err := cli.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("users")})
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if desc.Table == nil || aws.ToString(desc.Table.TableName) != "users" {
		t.Errorf("DescribeTable wrong table: %+v", desc.Table)
	}
	// Schema should report partition key 'id', no sort key.
	if len(desc.Table.KeySchema) != 1 || aws.ToString(desc.Table.KeySchema[0].AttributeName) != "id" {
		t.Errorf("DescribeTable schema = %+v", desc.Table.KeySchema)
	}

	// ListTables sees it.
	list, err := cli.ListTables(ctx, &dynamodb.ListTablesInput{})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	found := false
	for _, n := range list.TableNames {
		if n == "users" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListTables didn't include 'users': %v", list.TableNames)
	}

	// DeleteTable.
	if _, err := cli.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String("users")}); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	if _, err := cli.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("users")}); err == nil {
		t.Error("DescribeTable after delete: want error, got nil")
	}
}

func TestAWSSDK_DynamoDB_ItemCRUD_PartitionOnly(t *testing.T) {
	srv := harness.StartNoSQLServerAWS(t, inmem.New())
	cli := newDynamoDBClient(t, srv.URL)
	ctx := context.Background()

	mustCreateTable(t, cli, "kv", "id", "")

	// PutItem.
	if _, err := cli.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("kv"),
		Item: map[string]ddbtypes.AttributeValue{
			"id":   &ddbtypes.AttributeValueMemberS{Value: "a"},
			"name": &ddbtypes.AttributeValueMemberS{Value: "alice"},
			"age":  &ddbtypes.AttributeValueMemberN{Value: "30"},
			"vip":  &ddbtypes.AttributeValueMemberBOOL{Value: true},
			"bin":  &ddbtypes.AttributeValueMemberB{Value: []byte{0xde, 0xad}},
		},
	}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	// GetItem round-trip — every value type should survive.
	got, err := cli.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("kv"),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "a"},
		},
	})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if v, ok := got.Item["name"].(*ddbtypes.AttributeValueMemberS); !ok || v.Value != "alice" {
		t.Errorf("name = %+v, want S=alice", got.Item["name"])
	}
	if v, ok := got.Item["age"].(*ddbtypes.AttributeValueMemberN); !ok || v.Value != "30" {
		t.Errorf("age = %+v, want N=30", got.Item["age"])
	}
	if v, ok := got.Item["vip"].(*ddbtypes.AttributeValueMemberBOOL); !ok || !v.Value {
		t.Errorf("vip = %+v, want BOOL=true", got.Item["vip"])
	}
	if v, ok := got.Item["bin"].(*ddbtypes.AttributeValueMemberB); !ok || string(v.Value) != string([]byte{0xde, 0xad}) {
		t.Errorf("bin = %+v, want B=deadbeef", got.Item["bin"])
	}

	// GetItem on missing item returns empty Item (DynamoDB convention).
	miss, err := cli.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("kv"),
		Key:       map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "missing"}},
	})
	if err != nil {
		t.Fatalf("GetItem missing: %v", err)
	}
	if len(miss.Item) != 0 {
		t.Errorf("GetItem missing returned non-empty Item: %+v", miss.Item)
	}

	// DeleteItem.
	if _, err := cli.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String("kv"),
		Key:       map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "a"}},
	}); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	gone, err := cli.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("kv"),
		Key:       map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "a"}},
	})
	if err != nil {
		t.Fatalf("GetItem after delete: %v", err)
	}
	if len(gone.Item) != 0 {
		t.Errorf("GetItem after delete returned item: %+v", gone.Item)
	}
}

func TestAWSSDK_DynamoDB_QueryAndScan_CompositeKey(t *testing.T) {
	srv := harness.StartNoSQLServerAWS(t, inmem.New())
	cli := newDynamoDBClient(t, srv.URL)
	ctx := context.Background()

	mustCreateTable(t, cli, "events", "user", "ts")
	puts := []struct {
		user, ts, msg string
	}{
		{"u1", "2026-01-01", "a"},
		{"u1", "2026-01-02", "b"},
		{"u1", "2026-02-01", "c"},
		{"u2", "2026-01-01", "d"},
	}
	for _, p := range puts {
		if _, err := cli.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("events"),
			Item: map[string]ddbtypes.AttributeValue{
				"user": &ddbtypes.AttributeValueMemberS{Value: p.user},
				"ts":   &ddbtypes.AttributeValueMemberS{Value: p.ts},
				"msg":  &ddbtypes.AttributeValueMemberS{Value: p.msg},
			},
		}); err != nil {
			t.Fatalf("PutItem %+v: %v", p, err)
		}
	}

	// Query u1, no sort filter — expect 3 items.
	q, err := cli.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("events"),
		KeyConditionExpression: aws.String("#u = :u"),
		ExpressionAttributeNames: map[string]string{
			"#u": "user",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":u": &ddbtypes.AttributeValueMemberS{Value: "u1"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(q.Items) != 3 {
		t.Errorf("Query u1 len = %d, want 3", len(q.Items))
	}

	// Query u1 begins_with(ts, '2026-01') — expect 2 items.
	q2, err := cli.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("events"),
		KeyConditionExpression: aws.String("#u = :u AND begins_with(#t, :tp)"),
		ExpressionAttributeNames: map[string]string{
			"#u": "user",
			"#t": "ts",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":u":  &ddbtypes.AttributeValueMemberS{Value: "u1"},
			":tp": &ddbtypes.AttributeValueMemberS{Value: "2026-01"},
		},
	})
	if err != nil {
		t.Fatalf("Query begins_with: %v", err)
	}
	if len(q2.Items) != 2 {
		t.Errorf("Query u1 begins_with(2026-01) len = %d, want 2", len(q2.Items))
	}

	// Scan all — expect 4.
	s, err := cli.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("events")})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.Items) != 4 {
		t.Errorf("Scan len = %d, want 4", len(s.Items))
	}
}

func TestAWSSDK_DynamoDB_TagsRoundTrip(t *testing.T) {
	srv := harness.StartNoSQLServerAWS(t, inmem.New())
	cli := newDynamoDBClient(t, srv.URL)
	ctx := context.Background()

	mustCreateTable(t, cli, "tagged", "id", "")

	// Read back the ARN — synthetic but stable per name.
	desc, err := cli.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("tagged")})
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	arn := aws.ToString(desc.Table.TableArn)
	if arn == "" {
		t.Fatal("TableArn empty")
	}

	if _, err := cli.TagResource(ctx, &dynamodb.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags: []ddbtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("test")},
			{Key: aws.String("team"), Value: aws.String("data")},
		},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	list, err := cli.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsOfResource: %v", err)
	}
	got := map[string]string{}
	for _, tg := range list.Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}
	if got["env"] != "test" || got["team"] != "data" {
		t.Errorf("Tags after TagResource = %v", got)
	}

	if _, err := cli.UntagResource(ctx, &dynamodb.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	list2, err := cli.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsOfResource after untag: %v", err)
	}
	got = map[string]string{}
	for _, tg := range list2.Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}
	if _, ok := got["env"]; ok {
		t.Errorf("env tag still present after UntagResource: %v", got)
	}
	if got["team"] != "data" {
		t.Errorf("team tag missing after UntagResource: %v", got)
	}
}

func mustCreateTable(t *testing.T, cli *dynamodb.Client, name, pk, sk string) {
	t.Helper()
	attrs := []ddbtypes.AttributeDefinition{{
		AttributeName: aws.String(pk),
		AttributeType: ddbtypes.ScalarAttributeTypeS,
	}}
	keys := []ddbtypes.KeySchemaElement{{
		AttributeName: aws.String(pk),
		KeyType:       ddbtypes.KeyTypeHash,
	}}
	if sk != "" {
		attrs = append(attrs, ddbtypes.AttributeDefinition{
			AttributeName: aws.String(sk),
			AttributeType: ddbtypes.ScalarAttributeTypeS,
		})
		keys = append(keys, ddbtypes.KeySchemaElement{
			AttributeName: aws.String(sk),
			KeyType:       ddbtypes.KeyTypeRange,
		})
	}
	if _, err := cli.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName:            aws.String(name),
		AttributeDefinitions: attrs,
		KeySchema:            keys,
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	}); err != nil {
		t.Fatalf("CreateTable %s: %v", name, err)
	}
}
