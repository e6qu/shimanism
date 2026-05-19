package domain

import "fmt"

type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchInstance
	KindInstanceAlreadyExists
	KindInstanceNotAvailable
	KindInvalidArgument
)

type Error struct {
	Kind     Kind
	Resource string
	Message  string
	Inner    error
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNoSuchInstance:
		return fmt.Sprintf("cache instance %q does not exist", e.Resource)
	case KindInstanceAlreadyExists:
		return fmt.Sprintf("cache instance %q already exists", e.Resource)
	case KindInstanceNotAvailable:
		if e.Message != "" {
			return e.Message
		}
		return fmt.Sprintf("cache instance %q is not in an available state", e.Resource)
	case KindInvalidArgument:
		return e.Message
	default:
		if e.Message != "" {
			return e.Message
		}
		return "cache domain error"
	}
}

func (e *Error) Unwrap() error { return e.Inner }

func NoSuchInstance(name string) *Error {
	return &Error{Kind: KindNoSuchInstance, Resource: name}
}

func InstanceAlreadyExists(name string) *Error {
	return &Error{Kind: KindInstanceAlreadyExists, Resource: name}
}

func InstanceNotAvailable(name, message string) *Error {
	return &Error{Kind: KindInstanceNotAvailable, Resource: name, Message: message}
}

func InvalidArgument(message string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: message}
}
