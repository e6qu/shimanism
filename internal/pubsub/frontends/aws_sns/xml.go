package aws_sns

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"sort"
	"strings"
)

// writeXML emits the canonical awsQuery success envelope:
//
//	<{Action}Response xmlns="...">
//	  <{Action}Result>
//	    <Key1>Val1</Key1>
//	    ...
//	  </{Action}Result>
//	  <ResponseMetadata><RequestId>...</RequestId></ResponseMetadata>
//	</{Action}Response>
//
// pairs is a flat map of result-key → result-value emitted as
// <Key>Value</Key> children of the <Result>. For nested shapes
// (lists of members, attribute maps) use writeXMLStruct.
func writeXML(w http.ResponseWriter, status int, action string, pairs map[string]string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<` + action + `Response xmlns="` + snsNamespace + `">`)
	b.WriteString(`<` + action + `Result>`)
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := pairs[k]
		b.WriteString(`<` + k + `>`)
		_ = xml.EscapeText(&xmlBuilder{Builder: &b}, []byte(v))
		b.WriteString(`</` + k + `>`)
	}
	b.WriteString(`</` + action + `Result>`)
	b.WriteString(`<ResponseMetadata><RequestId>` + newRequestID() + `</RequestId></ResponseMetadata>`)
	b.WriteString(`</` + action + `Response>`)
	_, _ = w.Write([]byte(b.String()))
}

// writeXMLStruct emits the awsQuery envelope wrapping an arbitrary
// struct payload. The struct's XMLName drives the inner element
// shape; the wrapper supplies the <Response>/<Result> bookends.
func writeXMLStruct(w http.ResponseWriter, status int, action string, payload interface{}) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<` + action + `Response xmlns="` + snsNamespace + `">`)
	b.WriteString(`<` + action + `Result>`)
	out, _ := xml.Marshal(payload)
	_, _ = b.Write(out)
	b.WriteString(`</` + action + `Result>`)
	b.WriteString(`<ResponseMetadata><RequestId>` + newRequestID() + `</RequestId></ResponseMetadata>`)
	b.WriteString(`</` + action + `Response>`)
	_, _ = w.Write([]byte(b.String()))
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// xmlBuilder adapts strings.Builder to the io.Writer needed by
// xml.EscapeText without forcing an interface allocation.
type xmlBuilder struct{ *strings.Builder }

func (x *xmlBuilder) Write(p []byte) (int, error) {
	return x.Builder.Write(p)
}
