package awsquery

import (
	"context"
	"net/url"
)

// contextKey is unexported; the only way to get a Form back out of
// context is via FormFromContext.
type contextKey struct{}

// WithForm stores the parsed form values on the context. Generated
// handlers call this before dispatching to the backend so adapters
// that need fine-grained access to fields the emitter doesn't decode
// (e.g. map<string, struct> like SNS MessageAttributes) can retrieve
// them.
func WithForm(ctx context.Context, form url.Values) context.Context {
	return context.WithValue(ctx, contextKey{}, form)
}

// FormFromContext returns the form values stored by WithForm, or nil
// if the context wasn't set up by a generated awsQuery handler.
// Adapters that don't call this don't need to import url.Values; the
// emitter handles the common cases (scalars, string-keyed string and
// list-of-string maps) directly on the input struct.
func FormFromContext(ctx context.Context) url.Values {
	if v, ok := ctx.Value(contextKey{}).(url.Values); ok {
		return v
	}
	return nil
}
