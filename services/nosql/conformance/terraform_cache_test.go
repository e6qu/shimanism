package conformance_test

import (
	"os"
	"path/filepath"
)

func terraformPluginCacheDirForNoSQLWorkdir(dir string) string {
	d := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}
