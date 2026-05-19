package clientconfig

import (
	"strings"
	"testing"
)

func TestLoad_HasAllFrontends(t *testing.T) {
	o, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, want := range []string{"aws", "gcp", "azure"} {
		if _, ok := o.Frontends[want]; !ok {
			t.Errorf("missing frontend %q", want)
		}
	}
}

func TestLoad_AllShimmedServicesPresent(t *testing.T) {
	o, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	services := []string{"storage", "secrets", "queue", "pubsub", "rdbms", "cache", "functions", "apigateway"}
	for _, fe := range []string{"aws", "gcp", "azure"} {
		for _, svc := range services {
			if _, err := o.Lookup(fe, svc); err != nil {
				t.Errorf("missing (%s, %s): %v", fe, svc, err)
			}
		}
	}
}

func TestRenderShell_AWS_S3(t *testing.T) {
	o, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc, _ := o.Lookup("aws", "storage")
	out := svc.RenderShell("http://shim:9000")
	if !strings.Contains(out, `AWS_ENDPOINT_URL_S3="http://shim:9000"`) {
		t.Errorf("expected AWS_ENDPOINT_URL_S3 export; got: %s", out)
	}
}

func TestRenderTerraform_AWS_APIGW(t *testing.T) {
	o, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc, _ := o.Lookup("aws", "apigateway")
	out := svc.RenderTerraform("http://shim:9700")
	if !strings.Contains(out, `apigatewayv2 = "http://shim:9700"`) {
		t.Errorf("expected apigatewayv2 endpoint substitution; got: %s", out)
	}
}

func TestLookup_UnknownFrontend(t *testing.T) {
	o, _ := Load()
	if _, err := o.Lookup("ibm", "storage"); err == nil {
		t.Errorf("expected error for unknown frontend, got nil")
	}
}
