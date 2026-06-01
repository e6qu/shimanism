// Package aws is the AWS DynamoDB passthrough backend. It uses
// aws-sdk-go-v2/service/dynamodb to drive real DynamoDB (or a
// sockerless / per-request endpoint-overridden client for tests).
//
// **Stateless.** Every request is a thin translation from the shim's
// neutral domain.NoSQL methods onto DynamoDB API calls; no shim-side
// caches, no name → ARN tables, no schema rederivation. DynamoDB's
// own `DescribeTable` is the source of truth for partition / sort key
// names, used at PutItem / GetItem / DeleteItem time to map the
// neutral Key onto the DynamoDB AttributeValue key map.
//
// Attribute-value translation follows N19: domain.Value → DynamoDB
// AttributeValue is direct (String → S, Number-as-decimal-string → N,
// Bool → BOOL, Bytes → B, Null → NULL). Composite types (L / M / SS /
// NS / BS) are out of intersection and rejected at the boundary; the
// frontend already filters them, but the backend defends in depth.
package aws

import (
	"context"
	"errors"
	"sort"
	"strings"

	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"github.com/e6qu/shimanism/internal/nosql/domain"
)

// Backend implements domain.NoSQL via real AWS DynamoDB.
type Backend struct {
	c *ddb.Client
}

// New wraps an already-configured DynamoDB client.
func New(client *ddb.Client) *Backend { return &Backend{c: client} }

var _ domain.NoSQL = (*Backend)(nil)

// translateErr maps DynamoDB SDK errors to domain errors using the
// canonical DynamoDB error names from the Smithy spec.
func translateErr(err error, ctx string) error {
	if err == nil {
		return nil
	}
	var notFound *ddbtypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return domain.NoSuchTable(ctx)
	}
	var inUse *ddbtypes.ResourceInUseException
	if errors.As(err, &inUse) {
		return domain.TableAlreadyExists(ctx)
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ResourceNotFoundException":
			return domain.NoSuchTable(ctx)
		case "ResourceInUseException":
			return domain.TableAlreadyExists(ctx)
		case "ValidationException":
			return domain.InvalidArgument(apiErr.ErrorMessage())
		}
	}
	return err
}

// ---------------- Tables ----------------

func (b *Backend) CreateTable(ctx context.Context, name string, opt domain.CreateTableOptions) (domain.Table, error) {
	if opt.PartitionKeyName == "" {
		return domain.Table{}, domain.InvalidArgument("partition key name required")
	}
	attrs := []ddbtypes.AttributeDefinition{{
		AttributeName: strPtr(opt.PartitionKeyName),
		AttributeType: ddbtypes.ScalarAttributeTypeS,
	}}
	keySchema := []ddbtypes.KeySchemaElement{{
		AttributeName: strPtr(opt.PartitionKeyName),
		KeyType:       ddbtypes.KeyTypeHash,
	}}
	if opt.SortKeyName != "" {
		attrs = append(attrs, ddbtypes.AttributeDefinition{
			AttributeName: strPtr(opt.SortKeyName),
			AttributeType: ddbtypes.ScalarAttributeTypeS,
		})
		keySchema = append(keySchema, ddbtypes.KeySchemaElement{
			AttributeName: strPtr(opt.SortKeyName),
			KeyType:       ddbtypes.KeyTypeRange,
		})
	}
	in := &ddb.CreateTableInput{
		TableName:            strPtr(name),
		AttributeDefinitions: attrs,
		KeySchema:            keySchema,
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	}
	if len(opt.Tags) > 0 {
		in.Tags = make([]ddbtypes.Tag, 0, len(opt.Tags))
		for k, v := range opt.Tags {
			in.Tags = append(in.Tags, ddbtypes.Tag{Key: strPtr(k), Value: strPtr(v)})
		}
	}
	out, err := b.c.CreateTable(ctx, in)
	if err != nil {
		return domain.Table{}, translateErr(err, name)
	}
	return tableFromDescription(out.TableDescription, opt.Tags, opt.Description), nil
}

func (b *Backend) GetTable(ctx context.Context, name string) (domain.Table, error) {
	out, err := b.c.DescribeTable(ctx, &ddb.DescribeTableInput{TableName: strPtr(name)})
	if err != nil {
		return domain.Table{}, translateErr(err, name)
	}
	t := tableFromDescription(out.Table, nil, "")
	// Best-effort tag fetch; failure is non-fatal — empty tags is a
	// valid state and the spec doesn't require ListTagsOfResource to
	// succeed for DescribeTable callers.
	if out.Table != nil && out.Table.TableArn != nil {
		if tagsOut, terr := b.c.ListTagsOfResource(ctx, &ddb.ListTagsOfResourceInput{ResourceArn: out.Table.TableArn}); terr == nil && len(tagsOut.Tags) > 0 {
			tags := make(map[string]string, len(tagsOut.Tags))
			for _, tg := range tagsOut.Tags {
				if tg.Key != nil && tg.Value != nil {
					tags[*tg.Key] = *tg.Value
				}
			}
			t.Tags = tags
		}
	}
	return t, nil
}

func (b *Backend) UpdateTableTags(ctx context.Context, name string, tags map[string]string) error {
	t, err := b.GetTable(ctx, name)
	if err != nil {
		return err
	}
	if t.Name == "" {
		return domain.NoSuchTable(name)
	}
	// DynamoDB needs the ARN, not the name. DescribeTable returns it;
	// we re-fetch directly (rather than threading it through Table)
	// because keeping the domain.Table struct cloud-neutral matters
	// more than saving one call.
	desc, err := b.c.DescribeTable(ctx, &ddb.DescribeTableInput{TableName: strPtr(name)})
	if err != nil {
		return translateErr(err, name)
	}
	if desc.Table == nil || desc.Table.TableArn == nil {
		return domain.InvalidArgument("DescribeTable returned no TableArn for " + name)
	}
	arn := desc.Table.TableArn
	// Untag every existing key not in the new set, then tag every key
	// in the new set. DynamoDB's TagResource is upsert-style so we
	// could skip the untag step for keys present in both, but the
	// extra calls are a no-op price for clearer code.
	listed, err := b.c.ListTagsOfResource(ctx, &ddb.ListTagsOfResourceInput{ResourceArn: arn})
	if err != nil {
		return translateErr(err, name)
	}
	var toRemove []string
	for _, tg := range listed.Tags {
		if tg.Key == nil {
			continue
		}
		if _, keep := tags[*tg.Key]; !keep {
			toRemove = append(toRemove, *tg.Key)
		}
	}
	if len(toRemove) > 0 {
		if _, err := b.c.UntagResource(ctx, &ddb.UntagResourceInput{ResourceArn: arn, TagKeys: toRemove}); err != nil {
			return translateErr(err, name)
		}
	}
	if len(tags) > 0 {
		fresh := make([]ddbtypes.Tag, 0, len(tags))
		for k, v := range tags {
			fresh = append(fresh, ddbtypes.Tag{Key: strPtr(k), Value: strPtr(v)})
		}
		if _, err := b.c.TagResource(ctx, &ddb.TagResourceInput{ResourceArn: arn, Tags: fresh}); err != nil {
			return translateErr(err, name)
		}
	}
	return nil
}

func (b *Backend) DeleteTable(ctx context.Context, name string, force bool) error {
	if !force {
		// Honor the no-fakes rule: count items via Scan(limit=1) before
		// dropping the table. DynamoDB's own DeleteTable doesn't have a
		// "force" variant — both calls do the same thing — so the only
		// honest "not empty" surface is to refuse here.
		scan, err := b.c.Scan(ctx, &ddb.ScanInput{
			TableName: strPtr(name),
			Limit:     int32Ptr(1),
		})
		if err != nil {
			return translateErr(err, name)
		}
		if len(scan.Items) > 0 {
			return domain.TableNotEmpty(name)
		}
	}
	_, err := b.c.DeleteTable(ctx, &ddb.DeleteTableInput{TableName: strPtr(name)})
	if err != nil {
		return translateErr(err, name)
	}
	return nil
}

func (b *Backend) ListTables(ctx context.Context, opt domain.ListTablesOptions) (domain.ListTablesResult, error) {
	in := &ddb.ListTablesInput{}
	if opt.PageSize > 0 {
		in.Limit = int32Ptr(int32(opt.PageSize))
	}
	if opt.PageToken != "" {
		in.ExclusiveStartTableName = strPtr(opt.PageToken)
	}
	out, err := b.c.ListTables(ctx, in)
	if err != nil {
		return domain.ListTablesResult{}, translateErr(err, "")
	}
	res := domain.ListTablesResult{}
	for _, n := range out.TableNames {
		if opt.NamePrefix != "" && !strings.HasPrefix(n, opt.NamePrefix) {
			continue
		}
		// DescribeTable per result so the caller gets the full Table
		// shape (schema + tags). This is the cost of returning the
		// neutral form; consumers that only need names can use the
		// DynamoDB SDK directly.
		t, err := b.GetTable(ctx, n)
		if err != nil {
			return domain.ListTablesResult{}, err
		}
		res.Tables = append(res.Tables, t)
	}
	if out.LastEvaluatedTableName != nil {
		res.NextPageToken = *out.LastEvaluatedTableName
	}
	return res, nil
}

// ---------------- Items ----------------

func (b *Backend) PutItem(ctx context.Context, table string, item domain.Item) error {
	attrs, err := attrsToAWS(item.Attributes)
	if err != nil {
		return err
	}
	_, err = b.c.PutItem(ctx, &ddb.PutItemInput{
		TableName: strPtr(table),
		Item:      attrs,
	})
	return translateErr(err, table)
}

func (b *Backend) GetItem(ctx context.Context, table string, key domain.Key) (domain.Item, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.Item{}, err
	}
	keyMap, err := buildKeyMap(t, key)
	if err != nil {
		return domain.Item{}, err
	}
	out, err := b.c.GetItem(ctx, &ddb.GetItemInput{
		TableName: strPtr(table),
		Key:       keyMap,
	})
	if err != nil {
		return domain.Item{}, translateErr(err, table)
	}
	if len(out.Item) == 0 {
		return domain.Item{}, domain.NoSuchItem(table, describeKey(key))
	}
	return domain.Item{Attributes: attrsFromAWS(out.Item)}, nil
}

func (b *Backend) DeleteItem(ctx context.Context, table string, key domain.Key) error {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return err
	}
	keyMap, err := buildKeyMap(t, key)
	if err != nil {
		return err
	}
	out, err := b.c.DeleteItem(ctx, &ddb.DeleteItemInput{
		TableName:    strPtr(table),
		Key:          keyMap,
		ReturnValues: ddbtypes.ReturnValueAllOld,
	})
	if err != nil {
		return translateErr(err, table)
	}
	if len(out.Attributes) == 0 {
		return domain.NoSuchItem(table, describeKey(key))
	}
	return nil
}

func (b *Backend) Scan(ctx context.Context, table string, opt domain.ScanOptions) (domain.ScanResult, error) {
	in := &ddb.ScanInput{TableName: strPtr(table)}
	if opt.Limit > 0 {
		in.Limit = int32Ptr(int32(opt.Limit))
	}
	if opt.PageToken != "" {
		key, err := decodePageToken(opt.PageToken)
		if err != nil {
			return domain.ScanResult{}, err
		}
		in.ExclusiveStartKey = key
	}
	out, err := b.c.Scan(ctx, in)
	if err != nil {
		return domain.ScanResult{}, translateErr(err, table)
	}
	res := domain.ScanResult{}
	for _, raw := range out.Items {
		res.Items = append(res.Items, domain.Item{Attributes: attrsFromAWS(raw)})
	}
	if out.LastEvaluatedKey != nil {
		res.NextPageToken = encodePageToken(out.LastEvaluatedKey)
	}
	return res, nil
}

func (b *Backend) Query(ctx context.Context, table string, pk domain.Value, opt domain.QueryOptions) (domain.QueryResult, error) {
	t, err := b.GetTable(ctx, table)
	if err != nil {
		return domain.QueryResult{}, err
	}
	pkAV, err := valueToAWS(pk)
	if err != nil {
		return domain.QueryResult{}, err
	}
	expr := "#pk = :pk"
	exprNames := map[string]string{"#pk": t.PartitionKeyName}
	exprVals := map[string]ddbtypes.AttributeValue{":pk": pkAV}
	if opt.SortKeyPrefix != "" {
		if t.SortKeyName == "" {
			return domain.QueryResult{}, domain.InvalidArgument("SortKeyPrefix supplied but table " + table + " has no sort key")
		}
		expr += " AND begins_with(#sk, :sk)"
		exprNames["#sk"] = t.SortKeyName
		exprVals[":sk"] = &ddbtypes.AttributeValueMemberS{Value: opt.SortKeyPrefix}
	}
	in := &ddb.QueryInput{
		TableName:                 strPtr(table),
		KeyConditionExpression:    strPtr(expr),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprVals,
	}
	if opt.Limit > 0 {
		in.Limit = int32Ptr(int32(opt.Limit))
	}
	if opt.PageToken != "" {
		key, err := decodePageToken(opt.PageToken)
		if err != nil {
			return domain.QueryResult{}, err
		}
		in.ExclusiveStartKey = key
	}
	out, err := b.c.Query(ctx, in)
	if err != nil {
		return domain.QueryResult{}, translateErr(err, table)
	}
	res := domain.QueryResult{}
	for _, raw := range out.Items {
		res.Items = append(res.Items, domain.Item{Attributes: attrsFromAWS(raw)})
	}
	if out.LastEvaluatedKey != nil {
		res.NextPageToken = encodePageToken(out.LastEvaluatedKey)
	}
	return res, nil
}

// ---------------- Value translation (N19) ----------------

func valueToAWS(v domain.Value) (ddbtypes.AttributeValue, error) {
	switch v.Type {
	case domain.ValueString:
		return &ddbtypes.AttributeValueMemberS{Value: v.Str}, nil
	case domain.ValueNumber:
		return &ddbtypes.AttributeValueMemberN{Value: v.Num}, nil
	case domain.ValueBool:
		return &ddbtypes.AttributeValueMemberBOOL{Value: v.Bool}, nil
	case domain.ValueBytes:
		return &ddbtypes.AttributeValueMemberB{Value: append([]byte(nil), v.Bin...)}, nil
	case domain.ValueNull:
		return &ddbtypes.AttributeValueMemberNULL{Value: true}, nil
	}
	return nil, domain.Unsupported("attribute value type " + v.Type.String())
}

func valueFromAWS(av ddbtypes.AttributeValue) domain.Value {
	switch v := av.(type) {
	case *ddbtypes.AttributeValueMemberS:
		return domain.StringValue(v.Value)
	case *ddbtypes.AttributeValueMemberN:
		return domain.NumberValue(v.Value)
	case *ddbtypes.AttributeValueMemberBOOL:
		return domain.BoolValue(v.Value)
	case *ddbtypes.AttributeValueMemberB:
		return domain.BytesValue(v.Value)
	case *ddbtypes.AttributeValueMemberNULL:
		return domain.NullValue()
	}
	// Composite types (L / M / SS / NS / BS) are out of intersection.
	// Surface as Null so callers see "no value" rather than corrupted
	// data; the frontend filters them before reaching here.
	return domain.NullValue()
}

func attrsToAWS(in map[string]domain.Value) (map[string]ddbtypes.AttributeValue, error) {
	out := make(map[string]ddbtypes.AttributeValue, len(in))
	for k, v := range in {
		av, err := valueToAWS(v)
		if err != nil {
			return nil, err
		}
		out[k] = av
	}
	return out, nil
}

func attrsFromAWS(in map[string]ddbtypes.AttributeValue) map[string]domain.Value {
	out := make(map[string]domain.Value, len(in))
	for k, v := range in {
		out[k] = valueFromAWS(v)
	}
	return out
}

func buildKeyMap(t domain.Table, k domain.Key) (map[string]ddbtypes.AttributeValue, error) {
	if k.PartitionKey.Type == domain.ValueUnknown {
		return nil, domain.InvalidArgument("partition key required")
	}
	pkAV, err := valueToAWS(k.PartitionKey)
	if err != nil {
		return nil, err
	}
	m := map[string]ddbtypes.AttributeValue{t.PartitionKeyName: pkAV}
	if t.SortKeyName != "" {
		if k.SortKey.Type == domain.ValueUnknown {
			return nil, domain.InvalidArgument("table " + t.Name + " requires a sort key")
		}
		skAV, err := valueToAWS(k.SortKey)
		if err != nil {
			return nil, err
		}
		m[t.SortKeyName] = skAV
	} else if k.SortKey.Type != domain.ValueUnknown {
		return nil, domain.InvalidArgument("table " + t.Name + " has no sort key but one was supplied")
	}
	return m, nil
}

// ---------------- Table description translation ----------------

func tableFromDescription(td *ddbtypes.TableDescription, tags map[string]string, description string) domain.Table {
	t := domain.Table{Description: description, Tags: tags}
	if td == nil {
		return t
	}
	if td.TableName != nil {
		t.Name = *td.TableName
	}
	if td.CreationDateTime != nil {
		t.CreatedAt = *td.CreationDateTime
	}
	// Resolve partition / sort key names from KeySchema.
	for _, ks := range td.KeySchema {
		if ks.AttributeName == nil {
			continue
		}
		switch ks.KeyType {
		case ddbtypes.KeyTypeHash:
			t.PartitionKeyName = *ks.AttributeName
		case ddbtypes.KeyTypeRange:
			t.SortKeyName = *ks.AttributeName
		}
	}
	return t
}

// ---------------- Page-token codec ----------------

// DynamoDB's pagination token is an opaque map of AttributeValue. We
// re-serialise it as "<pk_name>=<encoded_value>[;<sk_name>=<encoded_value>]"
// to round-trip via the neutral string-typed PageToken field. The
// encoding is per-type: "s:<str>", "n:<num>", "b:1"/"b:0", "x:<base64>".
// Per the stateless-shim rule this encoding lives in code, not in
// any shim-side state.
func encodePageToken(k map[string]ddbtypes.AttributeValue) string {
	if len(k) == 0 {
		return ""
	}
	parts := make([]string, 0, len(k))
	names := make([]string, 0, len(k))
	for n := range k {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		parts = append(parts, n+"="+encodeValue(k[n]))
	}
	return strings.Join(parts, ";")
}

func decodePageToken(s string) (map[string]ddbtypes.AttributeValue, error) {
	if s == "" {
		return nil, nil
	}
	out := map[string]ddbtypes.AttributeValue{}
	for _, part := range strings.Split(s, ";") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return nil, domain.InvalidArgument("malformed page token segment: " + part)
		}
		name, enc := part[:eq], part[eq+1:]
		v, err := decodeValue(enc)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, nil
}

func encodeValue(av ddbtypes.AttributeValue) string {
	switch v := av.(type) {
	case *ddbtypes.AttributeValueMemberS:
		return "s:" + v.Value
	case *ddbtypes.AttributeValueMemberN:
		return "n:" + v.Value
	case *ddbtypes.AttributeValueMemberBOOL:
		if v.Value {
			return "b:1"
		}
		return "b:0"
	case *ddbtypes.AttributeValueMemberB:
		return "x:" + b64Encode(v.Value)
	case *ddbtypes.AttributeValueMemberNULL:
		return "_:null"
	}
	return "_:unknown"
}

func decodeValue(s string) (ddbtypes.AttributeValue, error) {
	if len(s) < 2 || s[1] != ':' {
		return nil, domain.InvalidArgument("malformed value encoding: " + s)
	}
	switch s[0] {
	case 's':
		return &ddbtypes.AttributeValueMemberS{Value: s[2:]}, nil
	case 'n':
		return &ddbtypes.AttributeValueMemberN{Value: s[2:]}, nil
	case 'b':
		return &ddbtypes.AttributeValueMemberBOOL{Value: s[2:] == "1"}, nil
	case 'x':
		b, err := b64Decode(s[2:])
		if err != nil {
			return nil, err
		}
		return &ddbtypes.AttributeValueMemberB{Value: b}, nil
	case '_':
		return &ddbtypes.AttributeValueMemberNULL{Value: true}, nil
	}
	return nil, domain.InvalidArgument("unknown value tag in page token: " + s[:1])
}

func describeKey(k domain.Key) string {
	parts := []string{encodeValueDomain(k.PartitionKey)}
	if k.SortKey.Type != domain.ValueUnknown {
		parts = append(parts, encodeValueDomain(k.SortKey))
	}
	return strings.Join(parts, "/")
}

func encodeValueDomain(v domain.Value) string {
	switch v.Type {
	case domain.ValueString:
		return v.Str
	case domain.ValueNumber:
		return v.Num
	case domain.ValueBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case domain.ValueBytes:
		return b64Encode(v.Bin)
	case domain.ValueNull:
		return "null"
	}
	return ""
}

// ---------------- tiny helpers ----------------

func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
