package azuresharedkey

import (
	"encoding/xml"
	"net/http"
	"os"
	"sync"
)

// Middleware returns an http.Handler that wraps `next`, verifying
// the incoming request's SharedKey signature before delegating.
// Failed verification short-circuits with Azure Storage's XML error
// envelope (the form `<?xml version="1.0"?><Error><Code>...</Code>
// <Message>...</Message></Error>`).
//
// Bypass gated on SHIMANISM_TEST_UNAUTHENTICATED=1.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bypass() {
				next.ServeHTTP(w, r)
				return
			}
			if err := v.Verify(r); err != nil {
				ve, ok := err.(*Error)
				if !ok {
					writeStorageError(w, http.StatusInternalServerError, "InternalError", err.Error())
					return
				}
				writeStorageError(w, ve.HTTPStatus, ve.Code, ve.Message)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// storageErrorEnvelope is the XML shape Azure Storage error responses take:
//
//	<?xml version="1.0" encoding="utf-8"?>
//	<Error>
//	  <Code>AuthenticationFailed</Code>
//	  <Message>...</Message>
//	</Error>
type storageErrorEnvelope struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func writeStorageError(w http.ResponseWriter, code int, errorCode, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-ms-error-code", errorCode)
	w.WriteHeader(code)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(storageErrorEnvelope{Code: errorCode, Message: message})
}

var (
	bypassOnce sync.Once
	bypassFlag bool
)

func bypass() bool {
	bypassOnce.Do(func() {
		bypassFlag = os.Getenv("SHIMANISM_TEST_UNAUTHENTICATED") == "1"
	})
	return bypassFlag
}
