package aws_rds

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
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
	b.WriteString(`<` + action + `Response xmlns="` + rdsNamespace + `">`)
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

func renderDBInstance(inst domain.Instance) string {
	endpointBlock := ""
	if inst.Status == domain.StatusAvailable && inst.Connection.Host != "" {
		endpointBlock = `<Endpoint><Address>` + escape(inst.Connection.Host) +
			`</Address><Port>` + strconv.Itoa(inst.Connection.Port) +
			`</Port></Endpoint>`
	}
	return `<DBInstance>` +
		`<DBInstanceIdentifier>` + escape(inst.Name) + `</DBInstanceIdentifier>` +
		`<Engine>` + escape(domainEngineToAWS(inst.Engine)) + `</Engine>` +
		`<EngineVersion>` + escape(inst.EngineVersion) + `</EngineVersion>` +
		`<DBInstanceStatus>` + escape(awsStatusFromDomain(inst.Status)) + `</DBInstanceStatus>` +
		`<DBInstanceClass>` + escape(inst.InstanceClass) + `</DBInstanceClass>` +
		`<AllocatedStorage>` + strconv.Itoa(inst.AllocatedStorageGB) + `</AllocatedStorage>` +
		`<MasterUsername>` + escape(inst.Connection.MasterUsername) + `</MasterUsername>` +
		`<DBName>` + escape(inst.Connection.DatabaseName) + `</DBName>` +
		endpointBlock +
		`</DBInstance>`
}

func writeDBInstanceResponse(w http.ResponseWriter, action string, inst domain.Instance) {
	writeXMLEnvelope(w, http.StatusOK, action, renderDBInstance(inst))
}

func writeDescribeDBInstances(w http.ResponseWriter, instances []domain.Instance) {
	var inner strings.Builder
	inner.WriteString(`<DBInstances>`)
	for _, i := range instances {
		inner.WriteString(renderDBInstance(i))
	}
	inner.WriteString(`</DBInstances>`)
	writeXMLEnvelope(w, http.StatusOK, "DescribeDBInstances", inner.String())
}

func renderDBSnapshot(s domain.Snapshot) string {
	return `<DBSnapshot>` +
		`<DBSnapshotIdentifier>` + escape(s.ID) + `</DBSnapshotIdentifier>` +
		`<DBInstanceIdentifier>` + escape(s.Instance) + `</DBInstanceIdentifier>` +
		`<Engine>` + escape(domainEngineToAWS(s.Engine)) + `</Engine>` +
		`<EngineVersion>` + escape(s.EngineVersion) + `</EngineVersion>` +
		`<Status>` + escape(awsStatusFromDomain(s.Status)) + `</Status>` +
		`</DBSnapshot>`
}

func writeDBSnapshotResponse(w http.ResponseWriter, action string, s domain.Snapshot) {
	writeXMLEnvelope(w, http.StatusOK, action, renderDBSnapshot(s))
}

func writeDescribeDBSnapshots(w http.ResponseWriter, snaps []domain.Snapshot) {
	var inner strings.Builder
	inner.WriteString(`<DBSnapshots>`)
	for _, s := range snaps {
		inner.WriteString(renderDBSnapshot(s))
	}
	inner.WriteString(`</DBSnapshots>`)
	writeXMLEnvelope(w, http.StatusOK, "DescribeDBSnapshots", inner.String())
}

type xmlBuilder struct{ *strings.Builder }

func (x *xmlBuilder) Write(p []byte) (int, error) { return x.Builder.Write(p) }
