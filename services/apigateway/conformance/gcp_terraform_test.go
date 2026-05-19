// Phase 8 conformance: GCP API Gateway-shaped frontend exercised
// by `hashicorp/google`. The hashicorp/google provider's per-
// service endpoint-override attribute for API Gateway changed
// names across major versions (`apigateway_custom_endpoint` vs
// `api_gateway_custom_endpoint`) and the GCP API Gateway resource
// lifecycle on the provider currently requires real OAuth-signed
// requests the shim's frontend doesn't simulate. Track A picks
// this up; for the mock matrix lane this conformance lives in
// the SDK + CLI tests.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_GCPAPIGateway_Smoke(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed (PATH lookup failed: %v)", err)
	}
	t.Skip("hashicorp/google per-resource endpoint override for API Gateway not yet wired (see BUGS.md)")
}
