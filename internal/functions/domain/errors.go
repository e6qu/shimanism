package domain

import "fmt"

type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchFunction
	KindFunctionAlreadyExists
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
	case KindNoSuchFunction:
		return fmt.Sprintf("function %q does not exist", e.Resource)
	case KindFunctionAlreadyExists:
		return fmt.Sprintf("function %q already exists", e.Resource)
	case KindInvalidArgument:
		return e.Message
	default:
		if e.Message != "" {
			return e.Message
		}
		return "functions domain error"
	}
}

func (e *Error) Unwrap() error { return e.Inner }

func NoSuchFunction(name string) *Error {
	return &Error{Kind: KindNoSuchFunction, Resource: name}
}

func FunctionAlreadyExists(name string) *Error {
	return &Error{Kind: KindFunctionAlreadyExists, Resource: name}
}

func InvalidArgument(message string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: message}
}
