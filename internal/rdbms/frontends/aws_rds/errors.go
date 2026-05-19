package aws_rds

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
)

type xmlErrorResponse struct {
	XMLName   xml.Name `xml:"ErrorResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	Error     xmlError `xml:"Error"`
	RequestId string   `xml:"RequestId"`
}

type xmlError struct {
	Type    string `xml:"Type"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func writeError(w http.ResponseWriter, status int, kind, code, message string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	resp := xmlErrorResponse{
		Xmlns: rdsNamespace,
		Error: xmlError{Type: kind, Code: code, Message: message},
	}
	out, _ := xml.MarshalIndent(resp, "", "  ")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "Receiver", "InternalError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchInstance:
		writeError(w, http.StatusNotFound, "Sender", "DBInstanceNotFound", de.Error())
	case domain.KindInstanceAlreadyExists:
		writeError(w, http.StatusBadRequest, "Sender", "DBInstanceAlreadyExists", de.Error())
	case domain.KindNoSuchSnapshot:
		writeError(w, http.StatusNotFound, "Sender", "DBSnapshotNotFound", de.Error())
	case domain.KindSnapshotAlreadyExists:
		writeError(w, http.StatusBadRequest, "Sender", "DBSnapshotAlreadyExists", de.Error())
	case domain.KindInstanceNotAvailable:
		writeError(w, http.StatusBadRequest, "Sender", "InvalidDBInstanceState", de.Error())
	case domain.KindUnsupportedEngine:
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameterValue", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "Sender", "InvalidParameterValue", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "Receiver", "InternalError", de.Error())
	}
}
