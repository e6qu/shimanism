package domain

import (
	"errors"
	"fmt"
)

// Kind is the discriminator on domain.Error. Frontend adapters use
// the Kind to pick the right cloud-vocabulary error envelope.
type Kind int

const (
	KindUnknown Kind = iota
	KindNoSuchBucket
	KindNoSuchKey
	KindNoSuchUpload
	KindBucketAlreadyExists
	KindBucketAlreadyOwnedByYou
	KindBucketNotEmpty
	KindInvalidArgument
	KindAccessDenied
	KindInternal
)

// Error is the typed error backends return. Frontend adapters
// detect it via errors.As and emit the right cloud-vocabulary
// envelope (S3's NoSuchBucket, GCS's notFound, Azure's
// ContainerNotFound, etc.). Use the package-level constructors below
// instead of building Error directly.
type Error struct {
	Kind     Kind
	Resource string // bucket name, "bucket/key", uploadID, etc.
	Message  string
	Inner    error
}

func (e *Error) Error() string {
	if e.Inner != nil {
		return fmt.Sprintf("%s: %s: %v", kindName(e.Kind), e.Resource, e.Inner)
	}
	return fmt.Sprintf("%s: %s", kindName(e.Kind), e.Resource)
}

func (e *Error) Unwrap() error { return e.Inner }

// Is allows errors.Is to compare against a sentinel Error{Kind: K}
// — useful for tests that want to check the kind without crafting a
// full Error value.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return e.Kind == t.Kind
}

func kindName(k Kind) string {
	switch k {
	case KindNoSuchBucket:
		return "NoSuchBucket"
	case KindNoSuchKey:
		return "NoSuchKey"
	case KindNoSuchUpload:
		return "NoSuchUpload"
	case KindBucketAlreadyExists:
		return "BucketAlreadyExists"
	case KindBucketAlreadyOwnedByYou:
		return "BucketAlreadyOwnedByYou"
	case KindBucketNotEmpty:
		return "BucketNotEmpty"
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindAccessDenied:
		return "AccessDenied"
	case KindInternal:
		return "InternalError"
	}
	return "Unknown"
}

// Constructor helpers

func NoSuchBucket(bucket string) *Error {
	return &Error{Kind: KindNoSuchBucket, Resource: bucket, Message: "no such bucket"}
}
func NoSuchKey(bucket, key string) *Error {
	return &Error{Kind: KindNoSuchKey, Resource: bucket + "/" + key, Message: "no such key"}
}
func NoSuchUpload(uploadID string) *Error {
	return &Error{Kind: KindNoSuchUpload, Resource: uploadID, Message: "no such upload"}
}
func BucketAlreadyExists(bucket string) *Error {
	return &Error{Kind: KindBucketAlreadyExists, Resource: bucket, Message: "bucket already exists"}
}
func BucketNotEmpty(bucket string) *Error {
	return &Error{Kind: KindBucketNotEmpty, Resource: bucket, Message: "bucket not empty"}
}
func InvalidArgument(msg string) *Error {
	return &Error{Kind: KindInvalidArgument, Message: msg}
}

// Sentinel values for errors.Is matching.
var (
	ErrNoSuchBucket   = &Error{Kind: KindNoSuchBucket}
	ErrNoSuchKey      = &Error{Kind: KindNoSuchKey}
	ErrNoSuchUpload   = &Error{Kind: KindNoSuchUpload}
	ErrBucketNotEmpty = &Error{Kind: KindBucketNotEmpty}
)
