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
	KindNoSuchTable
	KindTableAlreadyExists
	KindTableNotEmpty // DeleteTable(force=false) on a table with items
	KindNoSuchItem
	KindInvalidArgument
	KindUnsupported // out-of-intersection (e.g. transactions, secondary indexes, streams)
)

// Error is the structured failure type. Frontends translate the
// Kind to the source cloud's error envelope; backends translate
// the destination cloud's error envelope into this Kind.
type Error struct {
	Kind    Kind
	Name    string // table or key identifier, when known
	Message string
	Inner   error
}

func (e *Error) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("nosql %s: %s: %s", e.Kind, e.Name, e.Message)
	}
	return fmt.Sprintf("nosql %s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Inner }

func (k Kind) String() string {
	switch k {
	case KindNoSuchTable:
		return "NoSuchTable"
	case KindTableAlreadyExists:
		return "TableAlreadyExists"
	case KindTableNotEmpty:
		return "TableNotEmpty"
	case KindNoSuchItem:
		return "NoSuchItem"
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindUnsupported:
		return "Unsupported"
	default:
		return "Unknown"
	}
}

func NoSuchTable(name string) *Error {
	return &Error{Kind: KindNoSuchTable, Name: name, Message: "table not found"}
}

func TableAlreadyExists(name string) *Error {
	return &Error{Kind: KindTableAlreadyExists, Name: name, Message: "table already exists"}
}

func TableNotEmpty(name string) *Error {
	return &Error{Kind: KindTableNotEmpty, Name: name, Message: "table still has items"}
}

func NoSuchItem(table, keyDesc string) *Error {
	return &Error{Kind: KindNoSuchItem, Name: fmt.Sprintf("%s/%s", table, keyDesc), Message: "item not found"}
}

func InvalidArgument(msg string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: msg}
}

func Unsupported(feature string) *Error {
	return &Error{Kind: KindUnsupported, Message: feature + " is not in the cross-cloud NoSQL intersection"}
}

// IsKind reports whether err is a *domain.Error with the given Kind.
// Lets backends/frontends pattern-match by category.
func IsKind(err error, kind Kind) bool {
	var ne *Error
	if !errors.As(err, &ne) {
		return false
	}
	return ne.Kind == kind
}
