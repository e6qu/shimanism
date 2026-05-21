package awsjson_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/awsjson"
)

func TestEpochTime_MarshalsAsNumber(t *testing.T) {
	tt := awsjson.EpochTime(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	b, err := json.Marshal(tt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if got == "" || got[0] == '"' {
		t.Errorf("expected JSON number, got string: %q", got)
	}
	// Sanity: roughly 2026-05-21T12:00:00Z in epoch-seconds.
	if !strings.HasPrefix(got, "17") { // 2026 → 1.7-something billion
		t.Errorf("epoch encoding looks wrong: %q", got)
	}
}

func TestEpochTime_MarshalsZeroAsNull(t *testing.T) {
	var zero awsjson.EpochTime
	b, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(b) != "null" {
		t.Errorf("zero EpochTime marshal = %q, want null", string(b))
	}
}

func TestEpochTime_UnmarshalsNumber(t *testing.T) {
	var et awsjson.EpochTime
	if err := json.Unmarshal([]byte("1748000000"), &et); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if et.Time().IsZero() {
		t.Fatal("expected non-zero time after unmarshal")
	}
}

func TestEpochTime_UnmarshalsFractionalNumber(t *testing.T) {
	var et awsjson.EpochTime
	if err := json.Unmarshal([]byte("1748000000.5"), &et); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tt := et.Time()
	if tt.Nanosecond() != 500000000 {
		t.Errorf("expected 500ms fractional, got %d ns", tt.Nanosecond())
	}
}

func TestEpochTime_UnmarshalsNullAsZero(t *testing.T) {
	et := awsjson.EpochTime(time.Now())
	if err := json.Unmarshal([]byte("null"), &et); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !et.Time().IsZero() {
		t.Errorf("null EpochTime should produce zero time, got %v", et.Time())
	}
}

func TestEpochTime_UnmarshalsRFC3339Fallback(t *testing.T) {
	var et awsjson.EpochTime
	if err := json.Unmarshal([]byte(`"2026-05-21T12:00:00Z"`), &et); err != nil {
		t.Fatalf("unmarshal RFC3339: %v", err)
	}
	if et.Time().Year() != 2026 {
		t.Errorf("expected 2026, got %d", et.Time().Year())
	}
}

func TestEpochTimePtr_NilForZero(t *testing.T) {
	if got := awsjson.EpochTimePtr(time.Time{}); got != nil {
		t.Errorf("EpochTimePtr(zero) = %v, want nil", got)
	}
	if got := awsjson.EpochTimePtr(time.Now()); got == nil {
		t.Error("EpochTimePtr(now) = nil, want non-nil")
	}
}
