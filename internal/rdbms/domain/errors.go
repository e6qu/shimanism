package domain

import "fmt"

// Kind discriminates domain.Error values. Each frontend maps these
// to its cloud's error vocabulary; each backend produces them from
// its cloud's native error codes.
type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchInstance
	KindInstanceAlreadyExists
	KindNoSuchSnapshot
	KindSnapshotAlreadyExists
	KindInstanceNotAvailable
	KindUnsupportedEngine
	KindInvalidArgument
)

// Error is shimanism's neutral rdbms domain error.
type Error struct {
	Kind     Kind
	Resource string
	Message  string
	Inner    error
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNoSuchInstance:
		return fmt.Sprintf("DB instance %q does not exist", e.Resource)
	case KindInstanceAlreadyExists:
		return fmt.Sprintf("DB instance %q already exists", e.Resource)
	case KindNoSuchSnapshot:
		return fmt.Sprintf("DB snapshot %q does not exist", e.Resource)
	case KindSnapshotAlreadyExists:
		return fmt.Sprintf("DB snapshot %q already exists", e.Resource)
	case KindInstanceNotAvailable:
		if e.Message != "" {
			return e.Message
		}
		return fmt.Sprintf("DB instance %q is not in an available state", e.Resource)
	case KindUnsupportedEngine:
		if e.Message != "" {
			return e.Message
		}
		return "engine is not supported on this backend"
	case KindInvalidArgument:
		return e.Message
	default:
		if e.Message != "" {
			return e.Message
		}
		return "rdbms domain error"
	}
}

func (e *Error) Unwrap() error { return e.Inner }

func NoSuchInstance(name string) *Error {
	return &Error{Kind: KindNoSuchInstance, Resource: name}
}

func InstanceAlreadyExists(name string) *Error {
	return &Error{Kind: KindInstanceAlreadyExists, Resource: name}
}

func NoSuchSnapshot(id string) *Error {
	return &Error{Kind: KindNoSuchSnapshot, Resource: id}
}

func SnapshotAlreadyExists(id string) *Error {
	return &Error{Kind: KindSnapshotAlreadyExists, Resource: id}
}

func InstanceNotAvailable(name, message string) *Error {
	return &Error{Kind: KindInstanceNotAvailable, Resource: name, Message: message}
}

func UnsupportedEngine(message string) *Error {
	return &Error{Kind: KindUnsupportedEngine, Message: message}
}

func InvalidArgument(message string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: message}
}
