package emit

import _ "embed"

// fileTemplate holds the text/template source for the REST-XML wire
// protocol — the only protocol the emitter supported until Phase 11
// added awsJson1_1. Embedded as an asset (not a Go string literal) so
// the backticks in XML tag strings round-trip without escape
// gymnastics.
//
//go:embed template.tmpl
var fileTemplate string

// awsJSONTemplate holds the text/template source for the awsJson1_x
// wire protocols. Used when the service shape declares
// aws.protocols#awsJson1_1 (or #awsJson1_0). The protocol is single-
// endpoint POST `/` dispatched by `X-Amz-Target`; the template emits
// a different routing layer + JSON request/response encoding +
// awsJson-shaped error envelopes via internal/awsjson.
//
//go:embed template_awsjson.tmpl
var awsJSONTemplate string

// restJSONTemplate holds the text/template source for the restJson1
// wire protocol (Lambda, API Gateway v2). The protocol is HTTP-route
// dispatched (method + URI template from `smithy.api#http`, the same
// shape REST-XML uses) but the request / response bodies are JSON and
// the error envelope is awsJson-shaped (`__type` + `message`).
//
//go:embed template_restjson.tmpl
var restJSONTemplate string
