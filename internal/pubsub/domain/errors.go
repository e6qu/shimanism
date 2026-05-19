package domain

import "fmt"

// Kind discriminates domain.Error values. Each frontend maps these
// to its cloud's error vocabulary; each backend produces them from
// its cloud's native error codes.
type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchTopic
	KindTopicAlreadyExists
	KindNoSuchSubscription
	KindSubscriptionAlreadyExists
	KindInvalidReceiptHandle
	KindMessageTooLarge
	KindInvalidArgument
)

// Error is shimanism's neutral pub/sub domain error.
type Error struct {
	Kind     Kind
	Resource string
	Message  string
	Inner    error
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNoSuchTopic:
		return fmt.Sprintf("topic %q does not exist", e.Resource)
	case KindTopicAlreadyExists:
		return fmt.Sprintf("topic %q already exists", e.Resource)
	case KindNoSuchSubscription:
		return fmt.Sprintf("subscription %q does not exist", e.Resource)
	case KindSubscriptionAlreadyExists:
		return fmt.Sprintf("subscription %q already exists", e.Resource)
	case KindInvalidReceiptHandle:
		if e.Message != "" {
			return e.Message
		}
		return "invalid receipt handle"
	case KindMessageTooLarge:
		if e.Message != "" {
			return e.Message
		}
		return "message size exceeds topic maximum"
	case KindInvalidArgument:
		return e.Message
	default:
		if e.Message != "" {
			return e.Message
		}
		return "pubsub domain error"
	}
}

func (e *Error) Unwrap() error { return e.Inner }

func NoSuchTopic(name string) *Error {
	return &Error{Kind: KindNoSuchTopic, Resource: name}
}

func TopicAlreadyExists(name string) *Error {
	return &Error{Kind: KindTopicAlreadyExists, Resource: name}
}

func NoSuchSubscription(name string) *Error {
	return &Error{Kind: KindNoSuchSubscription, Resource: name}
}

func SubscriptionAlreadyExists(name string) *Error {
	return &Error{Kind: KindSubscriptionAlreadyExists, Resource: name}
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
