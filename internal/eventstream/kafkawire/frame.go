// Package kafkawire handles Kafka TCP frame boundaries and generated protocol
// request/response bodies for the event-streaming frontends.
package kafkawire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/twmb/franz-go/pkg/kmsg"
)

const DefaultMaxFrameSize = 100 * 1024 * 1024

var (
	ErrMalformedFrame     = errors.New("malformed kafka frame")
	ErrUnsupportedAPI     = errors.New("unsupported kafka api")
	ErrUnsupportedVersion = errors.New("unsupported kafka api version")
)

// RequestFrame is one decoded Kafka request payload. The frame length prefix is
// consumed before this struct is built.
type RequestFrame struct {
	APIKey        int16
	APIVersion    int16
	CorrelationID int32
	ClientID      *string
	Request       kmsg.Request
}

// ReadFrame reads one Kafka size-prefixed payload from r.
func ReadFrame(r io.Reader, maxSize int32) ([]byte, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxFrameSize
	}
	var sizeBuf [4]byte
	if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
		return nil, err
	}
	size := int32(binary.BigEndian.Uint32(sizeBuf[:]))
	if size < 0 {
		return nil, fmt.Errorf("negative kafka frame size %d: %w", size, ErrMalformedFrame)
	}
	if size > maxSize {
		return nil, fmt.Errorf("kafka frame size %d exceeds limit %d: %w", size, maxSize, ErrMalformedFrame)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeRequest decodes one Kafka request payload, excluding the 4-byte frame
// length prefix.
func DecodeRequest(payload []byte) (RequestFrame, error) {
	var cur cursor
	apiKey, err := cur.int16(payload)
	if err != nil {
		return RequestFrame{}, err
	}
	version, err := cur.int16(payload)
	if err != nil {
		return RequestFrame{}, err
	}
	correlationID, err := cur.int32(payload)
	if err != nil {
		return RequestFrame{}, err
	}

	req, err := requestFor(apiKey, version)
	if err != nil {
		return RequestFrame{}, err
	}

	var clientID *string
	if !(apiKey == 7 && version == 0) {
		clientID, err = cur.nullableString(payload)
		if err != nil {
			return RequestFrame{}, err
		}
		if req.IsFlexible() {
			if err := cur.skipTags(payload); err != nil {
				return RequestFrame{}, err
			}
		}
	}

	body := payload[cur.off:]
	if err := req.ReadFrom(body); err != nil {
		return RequestFrame{}, fmt.Errorf("decode kafka api %d v%d body: %w", apiKey, version, err)
	}
	if encoded := req.AppendTo(nil); len(encoded) != len(body) {
		return RequestFrame{}, fmt.Errorf("decode kafka api %d v%d body left trailing bytes: %w", apiKey, version, ErrMalformedFrame)
	}
	return RequestFrame{
		APIKey:        apiKey,
		APIVersion:    version,
		CorrelationID: correlationID,
		ClientID:      clientID,
		Request:       req,
	}, nil
}

// AppendResponse appends one Kafka size-prefixed response frame.
func AppendResponse(dst []byte, correlationID int32, resp kmsg.Response) []byte {
	lenAt := len(dst)
	dst = append(dst, 0, 0, 0, 0)
	dst = binary.BigEndian.AppendUint32(dst, uint32(correlationID))
	if resp.IsFlexible() {
		dst = append(dst, 0) // response header tagged fields
	}
	dst = resp.AppendTo(dst)
	binary.BigEndian.PutUint32(dst[lenAt:], uint32(len(dst)-lenAt-4))
	return dst
}

func requestFor(apiKey, version int16) (kmsg.Request, error) {
	var req kmsg.Request
	switch apiKey {
	case 0:
		v := kmsg.NewProduceRequest()
		req = &v
	case 1:
		v := kmsg.NewFetchRequest()
		req = &v
	case 2:
		v := kmsg.NewListOffsetsRequest()
		req = &v
	case 3:
		v := kmsg.NewMetadataRequest()
		req = &v
	case 8:
		v := kmsg.NewOffsetCommitRequest()
		req = &v
	case 9:
		v := kmsg.NewOffsetFetchRequest()
		req = &v
	case 10:
		v := kmsg.NewFindCoordinatorRequest()
		req = &v
	case 11:
		v := kmsg.NewJoinGroupRequest()
		req = &v
	case 12:
		v := kmsg.NewHeartbeatRequest()
		req = &v
	case 13:
		v := kmsg.NewLeaveGroupRequest()
		req = &v
	case 14:
		v := kmsg.NewSyncGroupRequest()
		req = &v
	case 18:
		v := kmsg.NewApiVersionsRequest()
		req = &v
	case 19:
		v := kmsg.NewCreateTopicsRequest()
		req = &v
	case 20:
		v := kmsg.NewDeleteTopicsRequest()
		req = &v
	default:
		return nil, fmt.Errorf("kafka api key %d: %w", apiKey, ErrUnsupportedAPI)
	}
	if version < 0 || version > req.MaxVersion() {
		return nil, fmt.Errorf("kafka api key %d version %d, max %d: %w", apiKey, version, req.MaxVersion(), ErrUnsupportedVersion)
	}
	req.SetVersion(version)
	return req, nil
}

type cursor struct {
	off int
}

func (c *cursor) int16(src []byte) (int16, error) {
	if len(src)-c.off < 2 {
		return 0, fmt.Errorf("need int16 at byte %d: %w", c.off, ErrMalformedFrame)
	}
	v := int16(binary.BigEndian.Uint16(src[c.off:]))
	c.off += 2
	return v, nil
}

func (c *cursor) int32(src []byte) (int32, error) {
	if len(src)-c.off < 4 {
		return 0, fmt.Errorf("need int32 at byte %d: %w", c.off, ErrMalformedFrame)
	}
	v := int32(binary.BigEndian.Uint32(src[c.off:]))
	c.off += 4
	return v, nil
}

func (c *cursor) nullableString(src []byte) (*string, error) {
	n, err := c.int16(src)
	if err != nil {
		return nil, err
	}
	if n == -1 {
		return nil, nil
	}
	if n < -1 {
		return nil, fmt.Errorf("invalid nullable string length %d at byte %d: %w", n, c.off-2, ErrMalformedFrame)
	}
	if len(src)-c.off < int(n) {
		return nil, fmt.Errorf("nullable string length %d exceeds remaining frame at byte %d: %w", n, c.off, ErrMalformedFrame)
	}
	out := string(src[c.off : c.off+int(n)])
	c.off += int(n)
	return &out, nil
}

func (c *cursor) skipTags(src []byte) error {
	count, err := c.uvarint(src)
	if err != nil {
		return err
	}
	for i := uint32(0); i < count; i++ {
		if _, err := c.uvarint(src); err != nil {
			return err
		}
		size, err := c.uvarint(src)
		if err != nil {
			return err
		}
		if size > uint32(len(src)-c.off) {
			return fmt.Errorf("tag size %d exceeds remaining frame at byte %d: %w", size, c.off, ErrMalformedFrame)
		}
		c.off += int(size)
	}
	return nil
}

func (c *cursor) uvarint(src []byte) (uint32, error) {
	var out uint32
	for shift := uint(0); shift <= 28; shift += 7 {
		if c.off >= len(src) {
			return 0, fmt.Errorf("unterminated uvarint at byte %d: %w", c.off, ErrMalformedFrame)
		}
		b := src[c.off]
		c.off++
		out |= uint32(b&0x7f) << shift
		if b&0x80 == 0 {
			return out, nil
		}
	}
	return 0, fmt.Errorf("uvarint exceeds %d: %w", math.MaxUint32, ErrMalformedFrame)
}
