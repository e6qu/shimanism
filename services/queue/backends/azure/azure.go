// Package azure is the Azure Service Bus queue backend for
// shimanism's queue service.
//
// Implementation note. Azure Service Bus's high-level Go SDK
// (`azservicebus`) requires the original *ReceivedMessage reference
// to call CompleteMessage / RenewMessageLock / AbandonMessage. The
// shim is stateless and can't hold the message between requests, so
// the data plane uses Azure SB's REST API directly for ack /
// extend-lock / abandon operations — those paths admit
// token-only acks via
//
//	DELETE /{queue}/messages/{messageId}/{lockToken}
//	POST   /{queue}/messages/{messageId}/{lockToken}
//
// SAS-token authentication is computed per-request from the
// configured connection string; the receipt handle is the
// composite "<messageId>|<lockToken>" so DeleteMessage /
// ChangeVisibility can reconstruct the URL without shim-side
// state.
//
// The Create/Delete/List/Head queue operations use the admin
// client; Send + Receive use the data-plane azservicebus SDK
// (which holds an AMQP link only for the duration of a single
// request) plus the REST API for the per-message ack hooks.
package azure

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

// iso8601 renders a duration in seconds as the ISO-8601 form Azure
// Service Bus accepts (e.g. PT30S, PT5M).
func iso8601(seconds int) string {
	return fmt.Sprintf("PT%dS", seconds)
}

// secondsFromISO8601 parses the subset of ISO-8601 durations Azure
// emits ("PT30S", "PT5M", "PT1H") and returns seconds. Returns 0
// for unrecognised forms.
func secondsFromISO8601(s string) int {
	if !strings.HasPrefix(s, "PT") {
		return 0
	}
	rest := strings.TrimPrefix(s, "PT")
	total := 0
	num := ""
	for _, c := range rest {
		switch {
		case c >= '0' && c <= '9':
			num += string(c)
		case c == 'H':
			n, _ := strconv.Atoi(num)
			total += n * 3600
			num = ""
		case c == 'M':
			n, _ := strconv.Atoi(num)
			total += n * 60
			num = ""
		case c == 'S':
			n, _ := strconv.Atoi(num)
			total += n
			num = ""
		}
	}
	return total
}

// Config holds Azure-specific knobs.
type Config struct {
	// ConnectionString is the SAS-style connection string Azure
	// hands out when you create a Service Bus namespace. Required.
	ConnectionString string
}

// Backend implements domain.Queues via Azure Service Bus.
type Backend struct {
	connStr  string
	endpoint string // https://<namespace>.servicebus.windows.net
	keyName  string
	key      []byte

	dataClient  *azservicebus.Client
	adminClient *admin.Client
	httpClient  *http.Client
}

// New constructs the backend from a connection string.
func New(cfg Config) (*Backend, error) {
	if cfg.ConnectionString == "" {
		return nil, fmt.Errorf("azservicebus: connection string is required")
	}
	endpoint, keyName, key, err := parseConnectionString(cfg.ConnectionString)
	if err != nil {
		return nil, err
	}
	dc, err := azservicebus.NewClientFromConnectionString(cfg.ConnectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("azservicebus: build data client: %w", err)
	}
	ac, err := admin.NewClientFromConnectionString(cfg.ConnectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("azservicebus: build admin client: %w", err)
	}
	return &Backend{
		connStr:     cfg.ConnectionString,
		endpoint:    endpoint,
		keyName:     keyName,
		key:         key,
		dataClient:  dc,
		adminClient: ac,
		httpClient:  http.DefaultClient,
	}, nil
}

var _ domain.Queues = (*Backend)(nil)

// parseConnectionString extracts the endpoint URL, SAS key name,
// and SAS key from an Azure Service Bus connection string.
func parseConnectionString(cs string) (endpoint, keyName string, key []byte, err error) {
	parts := strings.Split(cs, ";")
	var rawEndpoint, rawKeyName, rawKey string
	for _, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		k, v := p[:eq], p[eq+1:]
		switch k {
		case "Endpoint":
			rawEndpoint = strings.TrimPrefix(v, "sb://")
			rawEndpoint = strings.TrimSuffix(rawEndpoint, "/")
			rawEndpoint = "https://" + rawEndpoint
		case "SharedAccessKeyName":
			rawKeyName = v
		case "SharedAccessKey":
			rawKey = v
		}
	}
	if rawEndpoint == "" || rawKeyName == "" || rawKey == "" {
		return "", "", nil, fmt.Errorf("azservicebus: connection string missing Endpoint / SharedAccessKeyName / SharedAccessKey")
	}
	return rawEndpoint, rawKeyName, []byte(rawKey), nil
}

// sasToken signs an Azure Service Bus SAS token for the given
// resource URI, valid for the next 5 minutes.
func (b *Backend) sasToken(resource string) string {
	expiry := strconv.FormatInt(time.Now().Add(5*time.Minute).Unix(), 10)
	encoded := url.QueryEscape(resource)
	stringToSign := encoded + "\n" + expiry
	mac := hmac.New(sha256.New, b.key)
	mac.Write([]byte(stringToSign))
	sig := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return fmt.Sprintf("SharedAccessSignature sr=%s&sig=%s&se=%s&skn=%s",
		encoded, sig, expiry, b.keyName)
}

// receiptHandle encodes the (messageID, lockToken) pair so the
// shim can reconstruct the REST URL without holding state.
func encodeReceipt(messageID, lockToken string) string {
	return messageID + "|" + lockToken
}

func decodeReceipt(handle string) (messageID, lockToken string, ok bool) {
	i := strings.IndexByte(handle, '|')
	if i < 0 {
		return "", "", false
	}
	return handle[:i], handle[i+1:], true
}

func (b *Backend) restURL(queue, messageID, lockToken string) string {
	return fmt.Sprintf("%s/%s/messages/%s/%s", b.endpoint, queue, messageID, lockToken)
}

func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.StatusCode {
		case 404:
			return domain.NoSuchQueue(name)
		case 409:
			return domain.QueueAlreadyExists(name)
		}
	}
	if strings.Contains(err.Error(), "404") {
		return domain.NoSuchQueue(name)
	}
	if strings.Contains(err.Error(), "409") {
		return domain.QueueAlreadyExists(name)
	}
	return err
}

func (b *Backend) CreateQueue(ctx context.Context, name string, opt domain.CreateQueueOptions) (domain.Queue, error) {
	props := &admin.QueueProperties{}
	if opt.Attributes.VisibilityTimeoutSeconds > 0 {
		ld := iso8601(opt.Attributes.VisibilityTimeoutSeconds)
		props.LockDuration = &ld
	}
	if opt.Attributes.MessageRetentionSeconds > 0 {
		ttl := iso8601(opt.Attributes.MessageRetentionSeconds)
		props.DefaultMessageTimeToLive = &ttl
	}
	if _, err := b.adminClient.CreateQueue(ctx, name, &admin.CreateQueueOptions{Properties: props}); err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	return domain.Queue{Name: name, Attributes: opt.Attributes}, nil
}

func (b *Backend) DeleteQueue(ctx context.Context, name string) error {
	_, err := b.adminClient.DeleteQueue(ctx, name, nil)
	return translateErr(err, name)
}

func (b *Backend) HeadQueue(ctx context.Context, name string) (domain.Queue, error) {
	props, err := b.adminClient.GetQueue(ctx, name, nil)
	if err != nil {
		return domain.Queue{}, translateErr(err, name)
	}
	attrs := domain.QueueAttributes{}
	if props.LockDuration != nil {
		attrs.VisibilityTimeoutSeconds = secondsFromISO8601(*props.LockDuration)
	}
	if props.DefaultMessageTimeToLive != nil {
		attrs.MessageRetentionSeconds = secondsFromISO8601(*props.DefaultMessageTimeToLive)
	}
	if props.MaxSizeInMegabytes != nil {
		attrs.MaxMessageSizeBytes = int(*props.MaxSizeInMegabytes) * 1024 * 1024
	}
	return domain.Queue{Name: name, Attributes: attrs}, nil
}

func (b *Backend) ListQueues(ctx context.Context, opt domain.ListQueuesOptions) (domain.ListQueuesResult, error) {
	pager := b.adminClient.NewListQueuesPager(nil)
	res := domain.ListQueuesResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListQueuesResult{}, translateErr(err, "")
		}
		for _, q := range page.Queues {
			name := q.QueueName
			if opt.Prefix != "" && !strings.HasPrefix(name, opt.Prefix) {
				continue
			}
			attrs := domain.QueueAttributes{}
			if q.LockDuration != nil {
				attrs.VisibilityTimeoutSeconds = secondsFromISO8601(*q.LockDuration)
			}
			res.Queues = append(res.Queues, domain.Queue{Name: name, Attributes: attrs})
			if opt.MaxResults > 0 && len(res.Queues) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) SendMessage(ctx context.Context, queueName string, opt domain.SendMessageOptions) (domain.SendMessageResult, error) {
	sender, err := b.dataClient.NewSender(queueName, nil)
	if err != nil {
		return domain.SendMessageResult{}, translateErr(err, queueName)
	}
	defer func() { _ = sender.Close(ctx) }()
	msg := &azservicebus.Message{
		Body: append([]byte(nil), opt.Body...),
	}
	if len(opt.Attributes) > 0 {
		msg.ApplicationProperties = map[string]interface{}{}
		for k, v := range opt.Attributes {
			msg.ApplicationProperties[k] = v
		}
	}
	if err := sender.SendMessage(ctx, msg, nil); err != nil {
		return domain.SendMessageResult{}, translateErr(err, queueName)
	}
	// Azure doesn't expose a server-assigned message ID at send
	// time via this API path; the receiver sees one. Return the
	// client-side message ID if set, else empty.
	mid := ""
	if msg.MessageID != nil {
		mid = *msg.MessageID
	}
	return domain.SendMessageResult{MessageID: mid}, nil
}

func (b *Backend) ReceiveMessages(ctx context.Context, queueName string, opt domain.ReceiveMessagesOptions) ([]domain.Message, error) {
	recv, err := b.dataClient.NewReceiverForQueue(queueName, nil)
	if err != nil {
		return nil, translateErr(err, queueName)
	}
	defer func() { _ = recv.Close(ctx) }()
	maxN := opt.MaxMessages
	if maxN <= 0 || maxN > 10 {
		maxN = 10
	}
	rctx := ctx
	if opt.WaitTime > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, time.Duration(opt.WaitTime)*time.Second)
		defer cancel()
	}
	msgs, err := recv.ReceiveMessages(rctx, maxN, nil)
	if err != nil {
		// Receive often returns ctx-canceled on timeout — treat as
		// empty.
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, translateErr(err, queueName)
	}
	out := make([]domain.Message, 0, len(msgs))
	now := time.Now().UTC()
	for _, m := range msgs {
		var attrs map[string]string
		if len(m.ApplicationProperties) > 0 {
			attrs = make(map[string]string, len(m.ApplicationProperties))
			for k, v := range m.ApplicationProperties {
				attrs[k] = fmt.Sprint(v)
			}
		}
		// LockToken is a [16]byte UUID; format it canonically.
		lockToken := formatGUID(m.LockToken[:])
		mid := ""
		if m.MessageID != "" {
			mid = m.MessageID
		}
		out = append(out, domain.Message{
			MessageID:     mid,
			Body:          append([]byte(nil), m.Body...),
			Attributes:    attrs,
			ReceiptHandle: encodeReceipt(mid, lockToken),
			ReceivedAt:    now,
			DeliveryCount: int(m.DeliveryCount),
		})
	}
	return out, nil
}

// formatGUID renders a 16-byte LockToken as the dashed UUID form
// Azure expects in REST URLs.
func formatGUID(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (b *Backend) DeleteMessage(ctx context.Context, queueName string, receiptHandle string) error {
	mid, lock, ok := decodeReceipt(receiptHandle)
	if !ok {
		return domain.InvalidReceiptHandle("malformed receipt handle: " + receiptHandle)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, b.restURL(queueName, mid, lock), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.sasToken(b.endpoint+"/"+queueName))
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 404 {
			return domain.InvalidReceiptHandle(string(body))
		}
		return fmt.Errorf("azservicebus complete: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (b *Backend) ChangeVisibility(ctx context.Context, queueName string, receiptHandle string, visibilityTimeout int) error {
	mid, lock, ok := decodeReceipt(receiptHandle)
	if !ok {
		return domain.InvalidReceiptHandle("malformed receipt handle: " + receiptHandle)
	}
	// Azure REST `POST {queue}/messages/{messageId}/{lockToken}`
	// renews the lock to the queue's configured LockDuration (no
	// per-call timeout). The shim's per-call timeout is silently
	// ignored on this backend; documented in OPERATIONS.md.
	_ = visibilityTimeout
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.restURL(queueName, mid, lock), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.sasToken(b.endpoint+"/"+queueName))
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 404 {
			return domain.InvalidReceiptHandle(string(body))
		}
		return fmt.Errorf("azservicebus renew-lock: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// silence unused-import in early dev when paths drop a use.
var _ = time.Second
