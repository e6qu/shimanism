package restxml

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseString returns a pointer to s; "" sentinels nil. AWS bindings
// commonly emit a header / query param only when the value is set,
// so an empty value is treated as absent.
func ParseString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ParseInt32 parses an int32 query / header value. Returns (nil, nil)
// for the empty string; (nil, err) for a malformed value.
func ParseInt32(s string) (*int32, error) {
	if s == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("restxml: int32 %q: %w", s, err)
	}
	n32 := int32(n)
	return &n32, nil
}

// ParseInt64 parses an int64 query / header value.
func ParseInt64(s string) (*int64, error) {
	if s == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("restxml: int64 %q: %w", s, err)
	}
	return &n, nil
}

// ParseBool parses a bool query / header value. AWS accepts "true" /
// "false" (case-insensitive).
func ParseBool(s string) (*bool, error) {
	if s == "" {
		return nil, nil
	}
	switch strings.ToLower(s) {
	case "true":
		t := true
		return &t, nil
	case "false":
		f := false
		return &f, nil
	}
	return nil, fmt.Errorf("restxml: bool %q: not true or false", s)
}

// ParseTime decodes a timestamp using the Smithy timestamp-format
// hint. The supported formats are the ones the Smithy spec defines:
//
//	"date-time"      RFC 3339 with optional fractional seconds
//	"http-date"      RFC 7231 IMF-fixdate (HTTP Date header format)
//	"epoch-seconds"  fractional seconds since the Unix epoch
//
// An empty format defaults to "date-time" since that is AWS's REST-XML
// default for headers and bodies.
func ParseTime(s, format string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	switch format {
	case "", "date-time":
		t, err := time.Parse(time.RFC3339Nano, s)
		if err == nil {
			return &t, nil
		}
		// AWS often emits without fractional seconds: try RFC3339.
		t, err2 := time.Parse(time.RFC3339, s)
		if err2 == nil {
			return &t, nil
		}
		return nil, fmt.Errorf("restxml: date-time %q: %w", s, err)
	case "http-date":
		t, err := time.Parse(http.TimeFormat, s)
		if err != nil {
			return nil, fmt.Errorf("restxml: http-date %q: %w", s, err)
		}
		return &t, nil
	case "epoch-seconds":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("restxml: epoch-seconds %q: %w", s, err)
		}
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		t := time.Unix(sec, nsec).UTC()
		return &t, nil
	default:
		return nil, fmt.Errorf("restxml: unknown timestamp format %q", format)
	}
}

// FormatTime is the inverse of ParseTime.
func FormatTime(t time.Time, format string) string {
	switch format {
	case "", "date-time":
		return t.UTC().Format(time.RFC3339Nano)
	case "http-date":
		return t.UTC().Format(http.TimeFormat)
	case "epoch-seconds":
		s := float64(t.Unix()) + float64(t.Nanosecond())/1e9
		return strconv.FormatFloat(s, 'f', -1, 64)
	default:
		return t.UTC().Format(time.RFC3339Nano)
	}
}
