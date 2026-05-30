package domain

import (
	"errors"
	"fmt"
)

// Kind is the typed-error classification each cloud's frontend
// maps onto its native error envelope.
type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchZone
	KindZoneAlreadyExists
	KindZoneNotEmpty   // DeleteZone(force=false) on a zone with user-managed record sets
	KindNoSuchRecordSet
	KindInvalidArgument
	KindUnsupported // out-of-intersection (e.g. ALIAS records, vendor-specific routing policies)
)

// Error is the structured failure type. Frontends translate the
// Kind to the source cloud's error envelope; backends translate
// the destination cloud's error envelope into this Kind.
type Error struct {
	Kind    Kind
	Name    string // zone or record-set name, when known
	Message string
	Inner   error
}

func (e *Error) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("dns %s: %s: %s", e.Kind, e.Name, e.Message)
	}
	return fmt.Sprintf("dns %s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Inner }

func (k Kind) String() string {
	switch k {
	case KindNoSuchZone:
		return "NoSuchZone"
	case KindZoneAlreadyExists:
		return "ZoneAlreadyExists"
	case KindZoneNotEmpty:
		return "ZoneNotEmpty"
	case KindNoSuchRecordSet:
		return "NoSuchRecordSet"
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindUnsupported:
		return "Unsupported"
	default:
		return "Unknown"
	}
}

func NoSuchZone(name string) *Error {
	return &Error{Kind: KindNoSuchZone, Name: name, Message: "zone not found"}
}

func ZoneAlreadyExists(name string) *Error {
	return &Error{Kind: KindZoneAlreadyExists, Name: name, Message: "zone already exists"}
}

func ZoneNotEmpty(name string) *Error {
	return &Error{Kind: KindZoneNotEmpty, Name: name, Message: "zone still has user-managed record sets"}
}

func NoSuchRecordSet(zone, name string, rtype RecordType) *Error {
	return &Error{Kind: KindNoSuchRecordSet, Name: fmt.Sprintf("%s/%s/%s", zone, name, rtype), Message: "record set not found"}
}

func InvalidArgument(msg string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: msg}
}

func Unsupported(feature string) *Error {
	return &Error{Kind: KindUnsupported, Message: feature + " is not in the cross-cloud DNS intersection"}
}

// IsKind reports whether err is a *domain.Error with the given Kind.
// Lets backends/frontends pattern-match by category.
func IsKind(err error, kind Kind) bool {
	var de *Error
	if !errors.As(err, &de) {
		return false
	}
	return de.Kind == kind
}
