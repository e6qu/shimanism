package awsjson

import (
	"strconv"
	"time"
)

// EpochTime is the on-the-wire shape for timestamp fields in awsJson1_x
// (and aws-query) protocols: a JSON number representing seconds since
// the Unix epoch, with sub-second precision as decimal fraction. AWS
// SDKs round-trip this exact shape; emitting RFC3339 strings (Go's
// default time.Time JSON encoding) breaks them.
//
// Use as `*awsjson.EpochTime` in generated struct fields with the
// `smithy.api#timestampFormat` trait absent (the protocol default for
// awsJson1_x) or set to `epoch-seconds`. Zero / nil renders as `null`
// or omitted (omitempty).
type EpochTime time.Time

// MarshalJSON renders the time as the protocol's float64 epoch
// representation.
func (t EpochTime) MarshalJSON() ([]byte, error) {
	tt := time.Time(t)
	if tt.IsZero() {
		return []byte("null"), nil
	}
	seconds := float64(tt.UnixNano()) / 1e9
	return []byte(strconv.FormatFloat(seconds, 'f', -1, 64)), nil
}

// UnmarshalJSON accepts either a JSON number (the awsJson1_x wire
// format) or a JSON string (which some clients send during testing or
// when transcoding through other formats). On `null` the value stays
// zero.
func (t *EpochTime) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" || s == "" {
		*t = EpochTime(time.Time{})
		return nil
	}
	// Strip surrounding quotes for the string form.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Try RFC3339 as a last resort — some SDK middleware emits it
		// even when the spec says epoch-seconds.
		if rfc, errRfc := time.Parse(time.RFC3339Nano, s); errRfc == nil {
			*t = EpochTime(rfc.UTC())
			return nil
		}
		return err
	}
	sec, frac := int64(f), f-float64(int64(f))
	*t = EpochTime(time.Unix(sec, int64(frac*1e9)).UTC())
	return nil
}

// Time returns the underlying time.Time. Generated handlers + adapters
// use this to round-trip into the domain layer's time.Time fields.
func (t EpochTime) Time() time.Time { return time.Time(t) }

// EpochTimePtr is a convenience for adapters that build response
// values from `domain` types: pass a possibly-zero time.Time, get back
// nil if zero or a non-nil *EpochTime otherwise. Lets handler code
// stay declarative.
func EpochTimePtr(t time.Time) *EpochTime {
	if t.IsZero() {
		return nil
	}
	et := EpochTime(t.UTC())
	return &et
}
