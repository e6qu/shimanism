package aws_elasticache

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeXMLEnvelope(w http.ResponseWriter, status int, action, inner string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<` + action + `Response xmlns="` + ecNamespace + `">`)
	b.WriteString(`<` + action + `Result>`)
	b.WriteString(inner)
	b.WriteString(`</` + action + `Result>`)
	b.WriteString(`<ResponseMetadata><RequestId>` + newRequestID() + `</RequestId></ResponseMetadata>`)
	b.WriteString(`</` + action + `Response>`)
	_, _ = w.Write([]byte(b.String()))
}

func escape(s string) string {
	var sb strings.Builder
	_ = xml.EscapeText(&xmlBuilder{Builder: &sb}, []byte(s))
	return sb.String()
}

func renderCacheCluster(inst domain.Instance) string {
	endpointBlock := ""
	if inst.Status == domain.StatusAvailable && inst.Connection.Host != "" {
		endpointBlock = `<ConfigurationEndpoint><Address>` + escape(inst.Connection.Host) +
			`</Address><Port>` + strconv.Itoa(inst.Connection.Port) +
			`</Port></ConfigurationEndpoint>`
	}
	return `<CacheCluster>` +
		`<CacheClusterId>` + escape(inst.Name) + `</CacheClusterId>` +
		`<Engine>redis</Engine>` +
		`<EngineVersion>` + escape(inst.EngineVersion) + `</EngineVersion>` +
		`<CacheNodeType>` + escape(inst.NodeType) + `</CacheNodeType>` +
		`<CacheClusterStatus>` + escape(awsStatusFromDomain(inst.Status)) + `</CacheClusterStatus>` +
		endpointBlock +
		`</CacheCluster>`
}

func writeCacheClusterResponse(w http.ResponseWriter, action string, inst domain.Instance) {
	writeXMLEnvelope(w, http.StatusOK, action, renderCacheCluster(inst))
}

func writeDescribeCacheClusters(w http.ResponseWriter, instances []domain.Instance) {
	var inner strings.Builder
	inner.WriteString(`<CacheClusters>`)
	for _, i := range instances {
		inner.WriteString(renderCacheCluster(i))
	}
	inner.WriteString(`</CacheClusters>`)
	writeXMLEnvelope(w, http.StatusOK, "DescribeCacheClusters", inner.String())
}

type xmlBuilder struct{ *strings.Builder }

func (x *xmlBuilder) Write(p []byte) (int, error) { return x.Builder.Write(p) }
