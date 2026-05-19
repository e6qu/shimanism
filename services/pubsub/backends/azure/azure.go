// Package azure is the Azure Service Bus topics backend for
// shimanism's pubsub service.
//
// Same hybrid SDK + REST design as Phase 3's queue backend, but
// with topics + subscriptions:
//
//   - admin.Client for CreateTopic / DeleteTopic / CreateSubscription /
//     DeleteSubscription / ListTopics / ListSubscriptions.
//   - azservicebus.Client.NewSender(topic) for Publish.
//   - azservicebus.Client.NewReceiverForSubscription(topic, sub) for Receive.
//   - Raw HTTP REST + SAS-token signing for Complete + RenewLock,
//     because the high-level SDK requires *ReceivedMessage and the
//     shim is stateless. URL shape:
//     DELETE /{topic}/Subscriptions/{sub}/messages/{messageID}/{lockToken}
//     POST   /{topic}/Subscriptions/{sub}/messages/{messageID}/{lockToken}
//
// The receipt handle encodes "<topic>|<sub>|<messageID>|<lockToken>"
// so Ack / ChangeVisibility can reconstruct the URL without
// shim-side state.
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

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

type Config struct {
	ConnectionString string
}

type Backend struct {
	connStr  string
	endpoint string
	keyName  string
	key      []byte

	dataClient  *azservicebus.Client
	adminClient *admin.Client
	httpClient  *http.Client
}

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

var _ domain.Pubsub = (*Backend)(nil)

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

// encodeReceipt packs the (topic, sub, messageID, lockToken) tuple
// into a single opaque string for the shim's domain. ChangeVisibility
// + Ack reconstruct the REST URL from it.
func encodeReceipt(topic, sub, messageID, lockToken string) string {
	return topic + "|" + sub + "|" + messageID + "|" + lockToken
}

func decodeReceipt(handle string) (topic, sub, messageID, lockToken string, ok bool) {
	parts := strings.SplitN(handle, "|", 4)
	if len(parts) != 4 {
		return "", "", "", "", false
	}
	return parts[0], parts[1], parts[2], parts[3], true
}

func (b *Backend) restURL(topic, sub, messageID, lockToken string) string {
	return fmt.Sprintf("%s/%s/Subscriptions/%s/messages/%s/%s",
		b.endpoint, topic, sub, messageID, lockToken)
}

// formatLockToken renders Azure's 16-byte UUID lock token in the
// hyphenated form the REST API expects in URL paths.
func formatLockToken(b [16]byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func translateErr(err error, kind, name string) error {
	if err == nil {
		return nil
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.StatusCode {
		case 404:
			if kind == "topic" {
				return domain.NoSuchTopic(name)
			}
			return domain.NoSuchSubscription(name)
		case 409:
			if kind == "topic" {
				return domain.TopicAlreadyExists(name)
			}
			return domain.SubscriptionAlreadyExists(name)
		}
	}
	return err
}

// ----------------------------------------------------------------------
// Topics
// ----------------------------------------------------------------------

func (b *Backend) CreateTopic(ctx context.Context, name string, opt domain.CreateTopicOptions) (domain.Topic, error) {
	if _, err := b.adminClient.CreateTopic(ctx, name, nil); err != nil {
		return domain.Topic{}, translateErr(err, "topic", name)
	}
	return domain.Topic{Name: name, CreatedAt: time.Now().UTC()}, nil
}

func (b *Backend) DeleteTopic(ctx context.Context, name string) error {
	if _, err := b.adminClient.DeleteTopic(ctx, name, nil); err != nil {
		return translateErr(err, "topic", name)
	}
	return nil
}

func (b *Backend) HeadTopic(ctx context.Context, name string) (domain.Topic, error) {
	if _, err := b.adminClient.GetTopic(ctx, name, nil); err != nil {
		return domain.Topic{}, translateErr(err, "topic", name)
	}
	return domain.Topic{Name: name}, nil
}

func (b *Backend) ListTopics(ctx context.Context, opt domain.ListTopicsOptions) (domain.ListTopicsResult, error) {
	pager := b.adminClient.NewListTopicsPager(nil)
	res := domain.ListTopicsResult{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return res, translateErr(err, "topic", "")
		}
		for _, t := range page.Topics {
			if opt.Prefix != "" && !strings.HasPrefix(t.TopicName, opt.Prefix) {
				continue
			}
			res.Topics = append(res.Topics, domain.Topic{Name: t.TopicName})
			if opt.MaxResults > 0 && len(res.Topics) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

// ----------------------------------------------------------------------
// Subscriptions
// ----------------------------------------------------------------------

func (b *Backend) CreateSubscription(ctx context.Context, topic, sub string, opt domain.CreateSubscriptionOptions) (domain.Subscription, error) {
	if _, err := b.adminClient.CreateSubscription(ctx, topic, sub, nil); err != nil {
		var re *azcore.ResponseError
		if errors.As(err, &re) && re.StatusCode == 404 {
			return domain.Subscription{}, domain.NoSuchTopic(topic)
		}
		return domain.Subscription{}, translateErr(err, "subscription", sub)
	}
	return domain.Subscription{
		Name:               sub,
		Topic:              topic,
		AckDeadlineSeconds: opt.AckDeadlineSeconds,
		Durable:            true,
	}, nil
}

func (b *Backend) DeleteSubscription(ctx context.Context, sub string) error {
	// We need the owning topic to delete. Iterate topics until we
	// find one that owns this subscription.
	topic, err := b.findTopicForSubscription(ctx, sub)
	if err != nil {
		return err
	}
	if _, err := b.adminClient.DeleteSubscription(ctx, topic, sub, nil); err != nil {
		return translateErr(err, "subscription", sub)
	}
	return nil
}

func (b *Backend) HeadSubscription(ctx context.Context, sub string) (domain.Subscription, error) {
	topic, err := b.findTopicForSubscription(ctx, sub)
	if err != nil {
		return domain.Subscription{}, err
	}
	return domain.Subscription{Name: sub, Topic: topic, Durable: true}, nil
}

func (b *Backend) ListSubscriptions(ctx context.Context, opt domain.ListSubscriptionsOptions) (domain.ListSubscriptionsResult, error) {
	res := domain.ListSubscriptionsResult{}
	topicsToScan := []string{}
	if opt.Topic != "" {
		topicsToScan = []string{opt.Topic}
	} else {
		tres, err := b.ListTopics(ctx, domain.ListTopicsOptions{})
		if err != nil {
			return res, err
		}
		for _, t := range tres.Topics {
			topicsToScan = append(topicsToScan, t.Name)
		}
	}
	for _, t := range topicsToScan {
		pager := b.adminClient.NewListSubscriptionsPager(t, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return res, translateErr(err, "subscription", t)
			}
			for _, s := range page.Subscriptions {
				res.Subscriptions = append(res.Subscriptions, domain.Subscription{
					Name:    s.SubscriptionName,
					Topic:   t,
					Durable: true,
				})
				if opt.MaxResults > 0 && len(res.Subscriptions) >= opt.MaxResults {
					return res, nil
				}
			}
		}
	}
	return res, nil
}

func (b *Backend) findTopicForSubscription(ctx context.Context, sub string) (string, error) {
	tres, err := b.ListTopics(ctx, domain.ListTopicsOptions{})
	if err != nil {
		return "", err
	}
	for _, t := range tres.Topics {
		_, err := b.adminClient.GetSubscription(ctx, t.Name, sub, nil)
		if err == nil {
			return t.Name, nil
		}
	}
	return "", domain.NoSuchSubscription(sub)
}

// ----------------------------------------------------------------------
// Publish / Receive / Ack / ChangeVisibility
// ----------------------------------------------------------------------

func (b *Backend) Publish(ctx context.Context, topic string, opt domain.PublishOptions) (domain.PublishResult, error) {
	sender, err := b.dataClient.NewSender(topic, nil)
	if err != nil {
		return domain.PublishResult{}, translateErr(err, "topic", topic)
	}
	defer func() { _ = sender.Close(ctx) }()
	msg := &azservicebus.Message{
		Body: opt.Body,
	}
	if len(opt.Attributes) > 0 {
		msg.ApplicationProperties = map[string]interface{}{}
		for k, v := range opt.Attributes {
			msg.ApplicationProperties[k] = v
		}
	}
	if err := sender.SendMessage(ctx, msg, nil); err != nil {
		return domain.PublishResult{}, translateErr(err, "topic", topic)
	}
	return domain.PublishResult{MessageID: ""}, nil
}

func (b *Backend) Receive(ctx context.Context, sub string, opt domain.ReceiveOptions) ([]domain.Message, error) {
	topic, err := b.findTopicForSubscription(ctx, sub)
	if err != nil {
		return nil, err
	}
	recv, err := b.dataClient.NewReceiverForSubscription(topic, sub, nil)
	if err != nil {
		return nil, translateErr(err, "subscription", sub)
	}
	defer func() { _ = recv.Close(ctx) }()
	max := opt.MaxMessages
	if max <= 0 || max > 10 {
		max = 10
	}
	rctx := ctx
	if opt.WaitTime > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, time.Duration(opt.WaitTime)*time.Second)
		defer cancel()
	}
	msgs, err := recv.ReceiveMessages(rctx, max, nil)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, translateErr(err, "subscription", sub)
	}
	now := time.Now().UTC()
	out := make([]domain.Message, 0, len(msgs))
	for _, m := range msgs {
		attrs := map[string]string{}
		for k, v := range m.ApplicationProperties {
			if s, ok := v.(string); ok {
				attrs[k] = s
			}
		}
		out = append(out, domain.Message{
			MessageID:     m.MessageID,
			Body:          m.Body,
			Attributes:    attrs,
			ReceiptHandle: encodeReceipt(topic, sub, m.MessageID, formatLockToken(m.LockToken)),
			ReceivedAt:    now,
			DeliveryCount: int(m.DeliveryCount),
		})
	}
	return out, nil
}

func (b *Backend) Ack(ctx context.Context, sub string, receiptHandle string) error {
	topic, msub, messageID, lockToken, ok := decodeReceipt(receiptHandle)
	if !ok {
		return domain.InvalidReceiptHandle("malformed receipt handle")
	}
	_ = msub
	u := b.restURL(topic, sub, messageID, lockToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.sasToken(u))
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("azservicebus REST DELETE: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 410 {
		return domain.InvalidReceiptHandle("message lock has expired")
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("azservicebus REST DELETE: status=%d body=%s", resp.StatusCode, body)
	}
	return nil
}

func (b *Backend) ChangeVisibility(ctx context.Context, sub string, receiptHandle string, visibilityTimeout int) error {
	topic, msub, messageID, lockToken, ok := decodeReceipt(receiptHandle)
	if !ok {
		return domain.InvalidReceiptHandle("malformed receipt handle")
	}
	_ = msub
	_ = visibilityTimeout
	u := b.restURL(topic, sub, messageID, lockToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", b.sasToken(u))
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("azservicebus REST POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 410 {
		return domain.InvalidReceiptHandle("message lock has expired")
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("azservicebus REST POST: status=%d body=%s", resp.StatusCode, body)
	}
	return nil
}
