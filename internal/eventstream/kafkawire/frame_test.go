package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestDecodeRequest_ApiVersionsFlexible(t *testing.T) {
	req := kmsg.NewApiVersionsRequest()
	req.SetVersion(3)
	req.ClientSoftwareName = "shimanism-test"
	req.ClientSoftwareVersion = "20.0"

	wire := kmsg.NewRequestFormatter(kmsg.FormatterClientID("client-a")).AppendRequest(nil, &req, 42)
	payload, err := ReadFrame(bytes.NewReader(wire), DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	got, err := DecodeRequest(payload)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if got.APIKey != 18 || got.APIVersion != 3 || got.CorrelationID != 42 {
		t.Fatalf("unexpected header: %#v", got)
	}
	if got.ClientID == nil || *got.ClientID != "client-a" {
		t.Fatalf("client ID = %v, want client-a", got.ClientID)
	}
	body, ok := got.Request.(*kmsg.ApiVersionsRequest)
	if !ok {
		t.Fatalf("request type = %T, want *kmsg.ApiVersionsRequest", got.Request)
	}
	if body.ClientSoftwareName != "shimanism-test" || body.ClientSoftwareVersion != "20.0" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestDecodeRequest_MetadataWithHeaderTags(t *testing.T) {
	req := kmsg.NewMetadataRequest()
	req.SetVersion(9)
	req.Topics = []kmsg.MetadataRequestTopic{{Topic: kmsg.StringPtr("events")}}

	wire := kmsg.NewRequestFormatter(kmsg.FormatterClientID("client-b")).AppendRequest(nil, &req, 7)
	payload := append([]byte(nil), wire[4:]...)
	headerTagsAt := 2 + 2 + 4 + 2 + len("client-b")
	payload = append(payload[:headerTagsAt], append([]byte{1, 1, 2, 'x', 'y'}, payload[headerTagsAt+1:]...)...)

	got, err := DecodeRequest(payload)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	body, ok := got.Request.(*kmsg.MetadataRequest)
	if !ok {
		t.Fatalf("request type = %T, want *kmsg.MetadataRequest", got.Request)
	}
	if len(body.Topics) != 1 || body.Topics[0].Topic == nil || *body.Topics[0].Topic != "events" {
		t.Fatalf("topics = %#v, want events", body.Topics)
	}
}

func TestAppendResponse_ApiVersionsFlexible(t *testing.T) {
	resp := kmsg.NewApiVersionsResponse()
	resp.SetVersion(3)
	resp.ApiKeys = []kmsg.ApiVersionsResponseApiKey{
		{ApiKey: 18, MinVersion: 0, MaxVersion: 3},
		{ApiKey: 3, MinVersion: 0, MaxVersion: 9},
	}

	wire := AppendResponse(nil, 99, &resp)
	size := binary.BigEndian.Uint32(wire[:4])
	if int(size) != len(wire)-4 {
		t.Fatalf("size prefix = %d, want %d", size, len(wire)-4)
	}
	payload := wire[4:]
	if got := int32(binary.BigEndian.Uint32(payload[:4])); got != 99 {
		t.Fatalf("correlation ID = %d, want 99", got)
	}
	if payload[4] != 0 {
		t.Fatalf("flexible response header tags byte = %d, want 0", payload[4])
	}

	var decoded kmsg.ApiVersionsResponse
	decoded.SetVersion(3)
	if err := decoded.ReadFrom(payload[5:]); err != nil {
		t.Fatalf("ApiVersionsResponse.ReadFrom: %v", err)
	}
	if len(decoded.ApiKeys) != 2 || decoded.ApiKeys[0].ApiKey != 18 || decoded.ApiKeys[1].ApiKey != 3 {
		t.Fatalf("decoded response = %#v", decoded.ApiKeys)
	}
}

func TestReadFrameValidation(t *testing.T) {
	var negative bytes.Buffer
	_ = binary.Write(&negative, binary.BigEndian, int32(-1))
	if _, err := ReadFrame(&negative, DefaultMaxFrameSize); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("negative size error = %v, want malformed", err)
	}

	var tooLarge bytes.Buffer
	_ = binary.Write(&tooLarge, binary.BigEndian, int32(8))
	if _, err := ReadFrame(&tooLarge, 4); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("oversize error = %v, want malformed", err)
	}
}

func TestDecodeRequestValidation(t *testing.T) {
	unsupportedAPI := []byte{0, 99, 0, 0, 0, 0, 0, 1}
	if _, err := DecodeRequest(unsupportedAPI); !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatalf("unsupported api error = %v, want unsupported api", err)
	}

	unsupportedVersion := []byte{0, 18, 0, 99, 0, 0, 0, 1}
	if _, err := DecodeRequest(unsupportedVersion); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("unsupported version error = %v, want unsupported version", err)
	}

	truncated := []byte{0, 18, 0, 3, 0, 0, 0, 1, 0, 7, 'c'}
	if _, err := DecodeRequest(truncated); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("truncated header error = %v, want malformed", err)
	}

	req := kmsg.NewApiVersionsRequest()
	req.SetVersion(3)
	req.ClientSoftwareName = "shimanism-test"
	req.ClientSoftwareVersion = "20.0"
	trailing := kmsg.NewRequestFormatter(kmsg.FormatterClientID("client-a")).AppendRequest(nil, &req, 42)
	trailing = append(trailing[4:], 0)
	if _, err := DecodeRequest(trailing); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("trailing body error = %v, want malformed", err)
	}
}
