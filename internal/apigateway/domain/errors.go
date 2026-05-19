package domain

import "fmt"

type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchGateway
	KindGatewayAlreadyExists
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
	case KindNoSuchGateway:
		return fmt.Sprintf("gateway %q does not exist", e.Resource)
	case KindGatewayAlreadyExists:
		return fmt.Sprintf("gateway %q already exists", e.Resource)
	case KindInvalidArgument:
		return e.Message
	default:
		if e.Message != "" {
			return e.Message
		}
		return "apigateway domain error"
	}
}

func (e *Error) Unwrap() error { return e.Inner }

func NoSuchGateway(name string) *Error {
	return &Error{Kind: KindNoSuchGateway, Resource: name}
}

func GatewayAlreadyExists(name string) *Error {
	return &Error{Kind: KindGatewayAlreadyExists, Resource: name}
}

func InvalidArgument(message string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: message}
}
