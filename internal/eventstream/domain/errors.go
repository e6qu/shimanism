// Package domain holds the neutral event-streaming contract shared by
// Phase 20 frontends and backends.
package domain

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidInput  = errors.New("invalid input")
)

func TopicNotFound(name string) error {
	return fmt.Errorf("topic %q: %w", name, ErrNotFound)
}

func TopicAlreadyExists(name string) error {
	return fmt.Errorf("topic %q: %w", name, ErrAlreadyExists)
}

func InvalidArgument(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, ErrInvalidInput)...)
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists)
}

func IsInvalidInput(err error) bool {
	return errors.Is(err, ErrInvalidInput)
}
