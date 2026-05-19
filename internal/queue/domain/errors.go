package domain

import "fmt"

// Kind discriminates domain.Error values. Each frontend maps these
// to its cloud's error vocabulary; each backend produces them from
// its cloud's native error codes.
type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchQueue
	KindQueueAlreadyExists
	KindQueueBeingDeleted
	KindInvalidReceiptHandle
	KindMessageTooLarge
	KindInvalidArgument
)

// Error is shimanism's neutral queue-domain error.
type Error struct {
	Kind     Kind
	Resource string // queue name (or empty)
	Message  string
	Inner    error
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNoSuchQueue:
		return fmt.Sprintf("queue %q does not exist", e.Resource)
	case KindQueueAlreadyExists:
		return fmt.Sprintf("queue %q already exists", e.Resource)
	case KindQueueBeingDeleted:
		return fmt.Sprintf("queue %q is being deleted", e.Resource)
	case KindInvalidReceiptHandle:
		if e.Message != "" {
			return e.Message
		}
		return "invalid receipt handle"
	case KindMessageTooLarge:
		if e.Message != "" {
			return e.Message
		}
		return "message size exceeds queue maximum"
	case KindInvalidArgument:
		return e.Message
	default:
		if e.Message != "" {
			return e.Message
		}
		return "queue domain error"
	}
}

func (e *Error) Unwrap() error { return e.Inner }

func NoSuchQueue(name string) *Error {
	return &Error{Kind: KindNoSuchQueue, Resource: name}
}

func QueueAlreadyExists(name string) *Error {
	return &Error{Kind: KindQueueAlreadyExists, Resource: name}
}

func QueueBeingDeleted(name string) *Error {
	return &Error{Kind: KindQueueBeingDeleted, Resource: name}
}

func InvalidReceiptHandle(message string) *Error {
	return &Error{Kind: KindInvalidReceiptHandle, Message: message}
}

func MessageTooLarge(message string) *Error {
	return &Error{Kind: KindMessageTooLarge, Message: message}
}

func InvalidArgument(message string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: message}
}
