package domain

import "fmt"

// Kind discriminates domain.Error values. Each frontend maps these
// to its cloud's error vocabulary; each backend produces them from
// its cloud's native error codes.
type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchSecret
	KindNoSuchVersion
	KindSecretAlreadyExists
	KindSecretBeingDeleted
	KindInvalidArgument
)

// Error is shimanism's neutral secrets-domain error. Frontends use
// `errors.As(*domain.Error)` to translate to the source cloud's
// error envelope; backends construct these from their cloud's
// native error codes.
type Error struct {
	Kind     Kind
	Resource string // secret name (or empty)
	Version  uint64 // populated when Kind == KindNoSuchVersion
	Message  string
	Inner    error
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNoSuchSecret:
		return fmt.Sprintf("secret %q does not exist", e.Resource)
	case KindNoSuchVersion:
		return fmt.Sprintf("secret %q has no version %d", e.Resource, e.Version)
	case KindSecretAlreadyExists:
		return fmt.Sprintf("secret %q already exists", e.Resource)
	case KindSecretBeingDeleted:
		return fmt.Sprintf("secret %q is scheduled for deletion", e.Resource)
	case KindInvalidArgument:
		return e.Message
	default:
		if e.Message != "" {
			return e.Message
		}
		return "secrets domain error"
	}
}

func (e *Error) Unwrap() error { return e.Inner }

// Sentinel constructors. Used by backends and frontends so the
// {Kind, Resource} pair is consistent everywhere.

func NoSuchSecret(name string) *Error {
	return &Error{Kind: KindNoSuchSecret, Resource: name}
}

func NoSuchVersion(name string, version uint64) *Error {
	return &Error{Kind: KindNoSuchVersion, Resource: name, Version: version}
}

func SecretAlreadyExists(name string) *Error {
	return &Error{Kind: KindSecretAlreadyExists, Resource: name}
}

func SecretBeingDeleted(name string) *Error {
	return &Error{Kind: KindSecretBeingDeleted, Resource: name}
}

func InvalidArgument(message string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: message}
}
