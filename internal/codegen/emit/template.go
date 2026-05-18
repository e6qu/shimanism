package emit

import _ "embed"

// fileTemplate holds the text/template source for one generated Go
// file. Stored as an embedded .tmpl asset because real backticks in
// XML tag strings would otherwise need awkward escape gymnastics in a
// Go string literal.
//
//go:embed template.tmpl
var fileTemplate string
