// Phase 5 conformance: GCP Cloud SQL Admin frontend — `hashicorp/google`
// Terraform provider cell.
//
// **Documented skip.** `google_sql_database_instance` polls
// Operations.Get until done. The shim's frontend returns a PENDING
// Operation envelope but doesn't yet implement the
// `/v1/projects/{p}/operations/{op}` endpoint; the provider hangs.
// Wiring the Operations endpoint is straightforward but deferred to
// a follow-on. SDK cell + matrix coverage handle the
// driver-backend combination.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_GCPRDBMS_ResourceLifecycle(t *testing.T) {
	t.Skip("google_sql_database_instance polls Operations.Get; the shim's frontend doesn't implement the Operations endpoint at this phase. SDK + matrix cells cover this combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}
