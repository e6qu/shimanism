// Package aws_dynamodb is the AWS DynamoDB frontend adapter for
// shimanism's NoSQL service. It implements the generated
// gen.DynamoDBBackend interface (union of the 12 per-op interfaces
// the codegen emits) by translating each per-op request type into the
// neutral domain.NoSQL layer.
//
// The protocol is awsJson1_0 (X-Amz-Target = "DynamoDB_20120810.<Op>").
// The generated handlers own: decode, required-field validation,
// dispatch, encode, and JSON error envelope. This file owns: per-op
// translation logic, the ARN forging for tag operations, and the
// domain.Error → awsjson.BackendError mapper.
package aws_dynamodb

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/nosql/domain"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/nosql/gen"
)

// Adapter binds the generated DynamoDBBackend interface to a
// domain.NoSQL implementation.
type Adapter struct {
	n domain.NoSQL
}

var _ gen.DynamoDBBackend = (*Adapter)(nil)

// New returns an http.Handler dispatching DynamoDB requests through
// the generated awsJson1_0 router into the adapter bound to the
// given backend. Wrapped with the SigV4 verifier middleware; test
// mode honours SHIMANISM_TEST_UNAUTHENTICATED=1.
//
// Test-mode signing key — single AccessKey/Secret pair the shim
// trusts whenever the env var is not set. Real-cloud lanes wire
// their own CredentialStore.
func New(n domain.NoSQL) http.Handler {
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{
		Service: "dynamodb",
		Region:  "us-east-1",
	})
	mw := sigv4verifier.Middleware(verifier, awsjson.WriteError)
	return mw(gen.RegisterDynamoDBRoutes(&Adapter{n: n}))
}

// ----- helpers -----

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func boolPtr(b bool) *bool { return &b }

// tableARN forges a deterministic ARN from the table name. Real
// DynamoDB ARNs are arn:aws:dynamodb:<region>:<account>:table/<name>.
// We use a stable shim-internal account marker; SDK clients treat the
// ARN as an opaque string for tag operations.
func tableARN(name string) string {
	return "arn:aws:dynamodb:us-east-1:000000000000:table/" + name
}

// tableNameFromARN extracts the table name from a DynamoDB ARN. If
// the input doesn't look like an ARN we treat it as a bare name
// (the resource-name form is also accepted by real DynamoDB for some
// ops, but tag-resource APIs require ARN).
func tableNameFromARN(arn string) string {
	if i := strings.Index(arn, ":table/"); i >= 0 {
		return arn[i+len(":table/"):]
	}
	return arn
}

// ----- error mapping -----

func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return err
	}
	switch de.Kind {
	case domain.KindNoSuchTable, domain.KindNoSuchItem:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "ResourceNotFoundException",
			Message:    de.Message,
		}
	case domain.KindTableAlreadyExists:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "ResourceInUseException",
			Message:    de.Message,
		}
	case domain.KindTableNotEmpty:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "ResourceInUseException",
			Message:    de.Message,
		}
	case domain.KindInvalidArgument:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "ValidationException",
			Message:    de.Message,
		}
	case domain.KindUnsupported:
		return &awsjson.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "ValidationException",
			Message:    de.Message,
		}
	}
	return &awsjson.BackendError{
		HTTPStatus: http.StatusInternalServerError,
		Type:       "InternalServerError",
		Message:    de.Message,
	}
}

// ----- value translation (N19, the wire side) -----

func valueFromGen(av *gen.AttributeValue) (domain.Value, bool) {
	if av == nil {
		return domain.Value{}, false
	}
	switch {
	case av.S != nil:
		return domain.StringValue(*av.S), true
	case av.N != nil:
		return domain.NumberValue(*av.N), true
	case av.BOOL != nil:
		return domain.BoolValue(*av.BOOL), true
	case av.B != nil:
		return domain.BytesValue(av.B), true
	case av.NULL != nil && *av.NULL:
		return domain.NullValue(), true
	}
	// L / M / SS / NS / BS are out of intersection.
	return domain.Value{}, false
}

func valueToGen(v domain.Value) *gen.AttributeValue {
	switch v.Type {
	case domain.ValueString:
		s := v.Str
		return &gen.AttributeValue{S: &s}
	case domain.ValueNumber:
		n := v.Num
		return &gen.AttributeValue{N: &n}
	case domain.ValueBool:
		b := v.Bool
		return &gen.AttributeValue{BOOL: &b}
	case domain.ValueBytes:
		return &gen.AttributeValue{B: append([]byte(nil), v.Bin...)}
	case domain.ValueNull:
		t := true
		return &gen.AttributeValue{NULL: &t}
	}
	return nil
}

func attrsFromGen(m map[string]*gen.AttributeValue) (map[string]domain.Value, error) {
	out := make(map[string]domain.Value, len(m))
	for k, av := range m {
		v, ok := valueFromGen(av)
		if !ok {
			return nil, &awsjson.BackendError{
				HTTPStatus: http.StatusBadRequest,
				Type:       "ValidationException",
				Message:    "attribute " + k + " uses a composite type (L / M / SS / NS / BS) which is not in the shim's cross-cloud intersection",
			}
		}
		out[k] = v
	}
	return out, nil
}

func attrsToGen(in map[string]domain.Value) map[string]*gen.AttributeValue {
	out := make(map[string]*gen.AttributeValue, len(in))
	for k, v := range in {
		out[k] = valueToGen(v)
	}
	return out
}

// ----- table operations -----

func (a *Adapter) CreateTable(ctx context.Context, in *gen.CreateTableInput) (*gen.CreateTableOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	if len(in.KeySchema) == 0 {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "KeySchema is required"}
	}
	opt := domain.CreateTableOptions{}
	for _, ks := range in.KeySchema {
		if ks.AttributeName == "" {
			continue
		}
		switch ks.KeyType {
		case gen.KeyTypeHASH:
			opt.PartitionKeyName = ks.AttributeName
		case gen.KeyTypeRANGE:
			opt.SortKeyName = ks.AttributeName
		}
	}
	if opt.PartitionKeyName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "KeySchema must include a HASH key"}
	}
	if len(in.Tags) > 0 {
		opt.Tags = map[string]string{}
		for _, t := range in.Tags {
			if t.Key == "" {
				continue
			}
			opt.Tags[t.Key] = t.Value
		}
	}
	tbl, err := a.n.CreateTable(ctx, in.TableName, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateTableOutput{TableDescription: tableDescription(tbl)}, nil
}

func (a *Adapter) DeleteTable(ctx context.Context, in *gen.DeleteTableInput) (*gen.DeleteTableOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	tbl, err := a.n.GetTable(ctx, in.TableName)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	// DynamoDB's DeleteTable drops the table whether or not it has
	// items; mirror that on the wire by passing force=true.
	if err := a.n.DeleteTable(ctx, in.TableName, true); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteTableOutput{TableDescription: tableDescription(tbl)}, nil
}

func (a *Adapter) DescribeTable(ctx context.Context, in *gen.DescribeTableInput) (*gen.DescribeTableOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	tbl, err := a.n.GetTable(ctx, in.TableName)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DescribeTableOutput{Table: tableDescription(tbl)}, nil
}

func (a *Adapter) ListTables(ctx context.Context, in *gen.ListTablesInput) (*gen.ListTablesOutput, error) {
	opt := domain.ListTablesOptions{}
	if in.Limit != nil {
		opt.PageSize = int(*in.Limit)
	}
	if in.ExclusiveStartTableName != nil {
		opt.PageToken = *in.ExclusiveStartTableName
	}
	res, err := a.n.ListTables(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListTablesOutput{}
	for _, t := range res.Tables {
		out.TableNames = append(out.TableNames, t.Name)
	}
	if res.NextPageToken != "" {
		out.LastEvaluatedTableName = strPtr(res.NextPageToken)
	}
	return out, nil
}

// ----- item operations -----

func (a *Adapter) PutItem(ctx context.Context, in *gen.PutItemInput) (*gen.PutItemOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	attrs, err := attrsFromGen(in.Item)
	if err != nil {
		return nil, err
	}
	if err := a.n.PutItem(ctx, in.TableName, domain.Item{Attributes: attrs}); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.PutItemOutput{}, nil
}

func (a *Adapter) GetItem(ctx context.Context, in *gen.GetItemInput) (*gen.GetItemOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	key, err := keyFromGen(ctx, a, in.TableName, in.Key)
	if err != nil {
		return nil, err
	}
	item, err := a.n.GetItem(ctx, in.TableName, key)
	if err != nil {
		// DynamoDB's GetItem on a missing item returns 200 with empty
		// Item, NOT a 404. Translate NoSuchItem accordingly; other
		// errors still propagate.
		if domain.IsKind(err, domain.KindNoSuchItem) {
			return &gen.GetItemOutput{}, nil
		}
		return nil, mapDomainErr(err)
	}
	return &gen.GetItemOutput{Item: attrsToGen(item.Attributes)}, nil
}

func (a *Adapter) DeleteItem(ctx context.Context, in *gen.DeleteItemInput) (*gen.DeleteItemOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	key, err := keyFromGen(ctx, a, in.TableName, in.Key)
	if err != nil {
		return nil, err
	}
	if err := a.n.DeleteItem(ctx, in.TableName, key); err != nil {
		if domain.IsKind(err, domain.KindNoSuchItem) {
			// DynamoDB's DeleteItem is idempotent: missing item returns 200.
			return &gen.DeleteItemOutput{}, nil
		}
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteItemOutput{}, nil
}

func (a *Adapter) Scan(ctx context.Context, in *gen.ScanInput) (*gen.ScanOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	opt := domain.ScanOptions{}
	if in.Limit != nil {
		opt.Limit = int(*in.Limit)
	}
	if len(in.ExclusiveStartKey) > 0 {
		opt.PageToken = encodeStartKey(in.ExclusiveStartKey)
	}
	res, err := a.n.Scan(ctx, in.TableName, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ScanOutput{Count: int32Ptr(int32(len(res.Items))), ScannedCount: int32Ptr(int32(len(res.Items)))}
	for _, it := range res.Items {
		out.Items = append(out.Items, attrsToGen(it.Attributes))
	}
	if res.NextPageToken != "" {
		decoded, perr := decodeStartKey(res.NextPageToken)
		if perr == nil {
			out.LastEvaluatedKey = decoded
		}
	}
	return out, nil
}

func (a *Adapter) Query(ctx context.Context, in *gen.QueryInput) (*gen.QueryOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	// We require a KeyConditionExpression of the simple form
	// "<partition_attr> = :pk [AND begins_with(<sort_attr>, :sk)]".
	// More complex KeyConditions / KeyConditionExpression forms are
	// out-of-intersection — surface as ValidationException rather
	// than fabricating success.
	pkVal, skPrefix, perr := parseKeyCondition(in)
	if perr != nil {
		return nil, perr
	}
	opt := domain.QueryOptions{SortKeyPrefix: skPrefix}
	if in.Limit != nil {
		opt.Limit = int(*in.Limit)
	}
	if len(in.ExclusiveStartKey) > 0 {
		opt.PageToken = encodeStartKey(in.ExclusiveStartKey)
	}
	res, err := a.n.Query(ctx, in.TableName, pkVal, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.QueryOutput{Count: int32Ptr(int32(len(res.Items))), ScannedCount: int32Ptr(int32(len(res.Items)))}
	for _, it := range res.Items {
		out.Items = append(out.Items, attrsToGen(it.Attributes))
	}
	if res.NextPageToken != "" {
		decoded, perr := decodeStartKey(res.NextPageToken)
		if perr == nil {
			out.LastEvaluatedKey = decoded
		}
	}
	return out, nil
}

// ----- tag operations -----

func (a *Adapter) TagResource(ctx context.Context, in *gen.TagResourceInput) (struct{}, error) {
	if in.ResourceArn == "" {
		return struct{}{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "ResourceArn is required"}
	}
	name := tableNameFromARN(in.ResourceArn)
	tbl, err := a.n.GetTable(ctx, name)
	if err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	merged := map[string]string{}
	for k, v := range tbl.Tags {
		merged[k] = v
	}
	for _, t := range in.Tags {
		if t.Key == "" {
			continue
		}
		merged[t.Key] = t.Value
	}
	if err := a.n.UpdateTableTags(ctx, name, merged); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) UntagResource(ctx context.Context, in *gen.UntagResourceInput) (struct{}, error) {
	if in.ResourceArn == "" {
		return struct{}{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "ResourceArn is required"}
	}
	name := tableNameFromARN(in.ResourceArn)
	tbl, err := a.n.GetTable(ctx, name)
	if err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	remaining := map[string]string{}
	for k, v := range tbl.Tags {
		remaining[k] = v
	}
	for _, k := range in.TagKeys {
		delete(remaining, k)
	}
	if err := a.n.UpdateTableTags(ctx, name, remaining); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

// DescribeTimeToLive returns "TTL disabled" for every table. TTL is
// out of intersection for 15.C — DynamoDB / Firestore / Cosmos
// Tables all expose TTL, but the semantics differ (DynamoDB drives
// it from an attribute name + epoch-seconds value; Firestore via a
// per-collection rule; Cosmos via the document's `ttl` field).
// Returning DISABLED is the honest answer for the current shim.
func (a *Adapter) DescribeTimeToLive(ctx context.Context, in *gen.DescribeTimeToLiveInput) (*gen.DescribeTimeToLiveOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	if _, err := a.n.GetTable(ctx, in.TableName); err != nil {
		return nil, mapDomainErr(err)
	}
	disabled := gen.TimeToLiveStatusDISABLED
	return &gen.DescribeTimeToLiveOutput{
		TimeToLiveDescription: &gen.TimeToLiveDescription{TimeToLiveStatus: &disabled},
	}, nil
}

// DescribeContinuousBackups returns "PITR disabled" for every table.
// Point-in-Time Recovery is AWS-only and out of intersection.
func (a *Adapter) DescribeContinuousBackups(ctx context.Context, in *gen.DescribeContinuousBackupsInput) (*gen.DescribeContinuousBackupsOutput, error) {
	if in.TableName == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "TableName is required"}
	}
	if _, err := a.n.GetTable(ctx, in.TableName); err != nil {
		return nil, mapDomainErr(err)
	}
	disabledPITR := gen.PointInTimeRecoveryStatusDISABLED
	return &gen.DescribeContinuousBackupsOutput{
		ContinuousBackupsDescription: &gen.ContinuousBackupsDescription{
			ContinuousBackupsStatus: gen.ContinuousBackupsStatusDISABLED,
			PointInTimeRecoveryDescription: &gen.PointInTimeRecoveryDescription{
				PointInTimeRecoveryStatus: &disabledPITR,
			},
		},
	}, nil
}

func (a *Adapter) ListTagsOfResource(ctx context.Context, in *gen.ListTagsOfResourceInput) (*gen.ListTagsOfResourceOutput, error) {
	if in.ResourceArn == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "ResourceArn is required"}
	}
	name := tableNameFromARN(in.ResourceArn)
	tbl, err := a.n.GetTable(ctx, name)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.ListTagsOfResourceOutput{}
	for k, v := range tbl.Tags {
		out.Tags = append(out.Tags, gen.Tag{Key: k, Value: v})
	}
	return out, nil
}

// ----- helpers below this point -----

func int32Ptr(v int32) *int32 { return &v }

// keyFromGen builds a domain.Key from the wire-side Key map. It calls
// GetTable to learn the partition / sort key names — necessary
// because the wire-side Key map is name-keyed and the domain.Key is
// position-keyed.
func keyFromGen(ctx context.Context, a *Adapter, tableName string, wire map[string]*gen.AttributeValue) (domain.Key, error) {
	tbl, err := a.n.GetTable(ctx, tableName)
	if err != nil {
		return domain.Key{}, mapDomainErr(err)
	}
	pkAV, ok := wire[tbl.PartitionKeyName]
	if !ok {
		return domain.Key{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "key missing partition attribute " + tbl.PartitionKeyName}
	}
	pkVal, ok := valueFromGen(pkAV)
	if !ok {
		return domain.Key{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "partition key uses a composite attribute type"}
	}
	k := domain.Key{PartitionKey: pkVal}
	if tbl.SortKeyName != "" {
		skAV, ok := wire[tbl.SortKeyName]
		if !ok {
			return domain.Key{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "key missing sort attribute " + tbl.SortKeyName}
		}
		skVal, ok := valueFromGen(skAV)
		if !ok {
			return domain.Key{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "sort key uses a composite attribute type"}
		}
		k.SortKey = skVal
	}
	return k, nil
}

// encodeStartKey serialises a wire-side ExclusiveStartKey map to the
// neutral string PageToken the domain expects. The encoding is the
// same as the AWS-backend's encodePageToken for symmetry.
func encodeStartKey(k map[string]*gen.AttributeValue) string {
	parts := []string{}
	names := []string{}
	for n := range k {
		names = append(names, n)
	}
	// stable order
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		av := k[n]
		switch {
		case av.S != nil:
			parts = append(parts, n+"=s:"+*av.S)
		case av.N != nil:
			parts = append(parts, n+"=n:"+*av.N)
		case av.BOOL != nil:
			if *av.BOOL {
				parts = append(parts, n+"=b:1")
			} else {
				parts = append(parts, n+"=b:0")
			}
		case av.NULL != nil && *av.NULL:
			parts = append(parts, n+"=_:null")
		}
	}
	return strings.Join(parts, ";")
}

func decodeStartKey(s string) (map[string]*gen.AttributeValue, error) {
	if s == "" {
		return nil, nil
	}
	out := map[string]*gen.AttributeValue{}
	for _, part := range strings.Split(s, ";") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "malformed page token"}
		}
		name, enc := part[:eq], part[eq+1:]
		if len(enc) < 2 || enc[1] != ':' {
			return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "malformed page token value"}
		}
		val := enc[2:]
		switch enc[0] {
		case 's':
			s := val
			out[name] = &gen.AttributeValue{S: &s}
		case 'n':
			n := val
			out[name] = &gen.AttributeValue{N: &n}
		case 'b':
			b := val == "1"
			out[name] = &gen.AttributeValue{BOOL: &b}
		case '_':
			out[name] = &gen.AttributeValue{NULL: boolPtr(true)}
		default:
			return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "unknown page token tag"}
		}
	}
	return out, nil
}

// parseKeyCondition decodes the supported KeyConditionExpression
// shape into a partition-key value + optional sort-key prefix.
//
// Supported forms (after the SDK has done substitution of
// ExpressionAttributeNames / ExpressionAttributeValues):
//
//	"#pk = :pkv"
//	"#pk = :pkv AND begins_with(#sk, :skv)"
//	"pk_attr = :pkv"             (no name substitution)
//
// Anything more complex returns ValidationException. This is the
// honest "intersection-only" line — DynamoDB's full KeyConditionExpression
// grammar (>=, BETWEEN, <, >, <=) is not in the cross-cloud
// intersection (Firestore and Cosmos Tables expose narrower filters).
func parseKeyCondition(in *gen.QueryInput) (domain.Value, string, error) {
	if in.KeyConditionExpression == nil || *in.KeyConditionExpression == "" {
		return domain.Value{}, "", &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "KeyConditionExpression is required (KeyConditions form is not supported)"}
	}
	expr := *in.KeyConditionExpression
	pkPart, skPart, hasSort := splitOnAnd(expr)
	pkVal, _, err := decodeEqualsTerm(pkPart, in)
	if err != nil {
		return domain.Value{}, "", err
	}
	if !hasSort {
		return pkVal, "", nil
	}
	prefix, perr := decodeBeginsWith(skPart, in)
	if perr != nil {
		return domain.Value{}, "", perr
	}
	return pkVal, prefix, nil
}

func splitOnAnd(s string) (string, string, bool) {
	// Case-insensitive split on the first " AND " (with single spaces).
	upper := strings.ToUpper(s)
	idx := strings.Index(upper, " AND ")
	if idx < 0 {
		return strings.TrimSpace(s), "", false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+5:]), true
}

// decodeEqualsTerm parses "<name> = <value-or-placeholder>" returning
// the partition-key value and its attribute name.
func decodeEqualsTerm(term string, in *gen.QueryInput) (domain.Value, string, error) {
	parts := strings.SplitN(term, "=", 2)
	if len(parts) != 2 {
		return domain.Value{}, "", &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "unsupported KeyConditionExpression: " + term}
	}
	nameTok := strings.TrimSpace(parts[0])
	valTok := strings.TrimSpace(parts[1])
	if strings.HasPrefix(nameTok, "#") {
		resolved, ok := in.ExpressionAttributeNames[nameTok]
		if !ok {
			return domain.Value{}, "", &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "undefined name placeholder: " + nameTok}
		}
		nameTok = resolved
	}
	val, err := resolveValue(valTok, in)
	if err != nil {
		return domain.Value{}, "", err
	}
	return val, nameTok, nil
}

// decodeBeginsWith parses "begins_with(#sk, :skv)" returning the
// string prefix.
func decodeBeginsWith(term string, in *gen.QueryInput) (string, error) {
	t := strings.TrimSpace(term)
	if !strings.HasPrefix(strings.ToLower(t), "begins_with(") || !strings.HasSuffix(t, ")") {
		return "", &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "unsupported sort-key condition (only begins_with is in the intersection)"}
	}
	inner := strings.TrimSuffix(t[len("begins_with("):], ")")
	parts := strings.SplitN(inner, ",", 2)
	if len(parts) != 2 {
		return "", &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "malformed begins_with"}
	}
	valTok := strings.TrimSpace(parts[1])
	val, err := resolveValue(valTok, in)
	if err != nil {
		return "", err
	}
	if val.Type != domain.ValueString {
		return "", &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "begins_with only supports string prefixes (N19 intersection)"}
	}
	return val.Str, nil
}

func resolveValue(tok string, in *gen.QueryInput) (domain.Value, error) {
	if strings.HasPrefix(tok, ":") {
		av, ok := in.ExpressionAttributeValues[tok]
		if !ok {
			return domain.Value{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "undefined value placeholder: " + tok}
		}
		v, ok := valueFromGen(av)
		if !ok {
			return domain.Value{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "placeholder " + tok + " uses a composite type"}
		}
		return v, nil
	}
	return domain.Value{}, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "ValidationException", Message: "literal values in KeyConditionExpression are not supported; use a placeholder"}
}

// tableDescription renders a domain.Table back into the wire-side
// TableDescription envelope.
func tableDescription(t domain.Table) *gen.TableDescription {
	td := &gen.TableDescription{
		TableName:   strPtr(t.Name),
		TableArn:    strPtr(tableARN(t.Name)),
		TableStatus: tableStatusPtr(gen.TableStatusACTIVE),
	}
	if t.PartitionKeyName != "" {
		td.AttributeDefinitions = append(td.AttributeDefinitions, gen.AttributeDefinition{
			AttributeName: t.PartitionKeyName,
			AttributeType: gen.ScalarAttributeTypeS,
		})
		td.KeySchema = append(td.KeySchema, gen.KeySchemaElement{
			AttributeName: t.PartitionKeyName,
			KeyType:       gen.KeyTypeHASH,
		})
	}
	if t.SortKeyName != "" {
		td.AttributeDefinitions = append(td.AttributeDefinitions, gen.AttributeDefinition{
			AttributeName: t.SortKeyName,
			AttributeType: gen.ScalarAttributeTypeS,
		})
		td.KeySchema = append(td.KeySchema, gen.KeySchemaElement{
			AttributeName: t.SortKeyName,
			KeyType:       gen.KeyTypeRANGE,
		})
	}
	if !t.CreatedAt.IsZero() {
		et := awsjson.EpochTime(t.CreatedAt)
		td.CreationDateTime = &et
	}
	return td
}

func tableStatusPtr(s gen.TableStatus) *gen.TableStatus { return &s }
