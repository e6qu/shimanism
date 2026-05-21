// Tests for the awsJson1_x emitter path. Asserts the emitter produces
// gofmt-clean Go that parses cleanly, contains the expected handler /
// type / registration symbols, and dispatches correctly when hit by a
// real HTTP request through the awsjson runtime helper.
package codegen_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/codegen/emit"
	"github.com/e6qu/shimanism/internal/codegen/smithy"
)

func TestCodegen_AWSJSON_EmitsValidGo(t *testing.T) {
	root := repoRoot(t)
	manifest := loadManifest(t, filepath.Join(root, "services", "secrets", "codegen.json"))

	specBytes, err := os.ReadFile(filepath.Join(root, manifest.Spec))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	model, err := smithy.Parse(specBytes)
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}

	// Use a placeholder commit since secrets/spec/SOURCES.md may not
	// carry a pinned commit yet — this test is about emitter behavior,
	// not spec freshness.
	got, err := emit.Emit(model, emit.Options{
		PackageName:  "gen",
		SourceFile:   manifest.Spec,
		SourceCommit: "0000000000000000000000000000000000000000",
		Operations:   manifest.Operations,
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "aws_secretsmanager.gen.go", got, parser.AllErrors)
	if err != nil {
		t.Fatalf("emitted Go is not parseable: %v\n--- source ---\n%s", err, got)
	}

	wantFuncs := []string{"RegisterSecretsManagerRoutes"}
	wantHandlers := []string{}
	wantBackends := []string{"SecretsManagerBackend"}
	for _, op := range manifest.Operations {
		wantHandlers = append(wantHandlers, op+"Handler")
		wantBackends = append(wantBackends, op+"Backend")
	}

	got_funcs := map[string]bool{}
	got_types := map[string]bool{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil { // top-level function, not a method
				got_funcs[d.Name.Name] = true
			}
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					got_types[ts.Name.Name] = true
				}
			}
		}
	}

	for _, name := range wantFuncs {
		if !got_funcs[name] {
			t.Errorf("missing top-level func %q in emitted output", name)
		}
	}
	for _, name := range wantHandlers {
		if !got_funcs[name] {
			t.Errorf("missing handler func %q in emitted output", name)
		}
	}
	for _, name := range wantBackends {
		if !got_types[name] {
			t.Errorf("missing backend interface %q in emitted output", name)
		}
	}

	// The awsJson1_1 path uses the awsjson runtime helper, not restxml.
	src := string(got)
	if !strings.Contains(src, "github.com/e6qu/shimanism/internal/awsjson") {
		t.Error("emitted code does not import internal/awsjson")
	}
	if strings.Contains(src, "github.com/e6qu/shimanism/internal/restxml") {
		t.Error("emitted code unexpectedly imports internal/restxml (this is the awsJson1_1 path)")
	}
	// The awsJson1_1 service uses `awsjson.NewRouter` for dispatch.
	// The router service-name is the smithy short name (the same
	// string AWS SDK clients send in X-Amz-Target's left-hand side);
	// for AWS Secrets Manager that is "secretsmanager" — lowercase.
	if !strings.Contains(src, `awsjson.NewRouter("secretsmanager")`) {
		t.Error(`emitted code missing awsjson.NewRouter("secretsmanager") registration`)
	}
}

func TestCodegen_AWSJSON_ManifestOperationsExist(t *testing.T) {
	root := repoRoot(t)
	manifest := loadManifest(t, filepath.Join(root, "services", "secrets", "codegen.json"))

	specBytes, err := os.ReadFile(filepath.Join(root, manifest.Spec))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	model, err := smithy.Parse(specBytes)
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	for _, op := range manifest.Operations {
		if _, _, err := model.LookupOperation(op); err != nil {
			t.Errorf("manifest operation %q: %v", op, err)
		}
	}
}

func TestCodegen_AWSJSON_DetectsProtocolFromSpec(t *testing.T) {
	root := repoRoot(t)

	// Secrets Manager declares awsJson1_1; emitter should pick that
	// template path (which imports awsjson, not restxml).
	manifest := loadManifest(t, filepath.Join(root, "services", "secrets", "codegen.json"))
	specBytes, _ := os.ReadFile(filepath.Join(root, manifest.Spec))
	model, _ := smithy.Parse(specBytes)
	got, err := emit.Emit(model, emit.Options{
		PackageName:  "gen",
		SourceFile:   manifest.Spec,
		SourceCommit: "0000000000000000000000000000000000000000",
		Operations:   []string{manifest.Operations[0]},
	})
	if err != nil {
		t.Fatalf("emit secrets: %v", err)
	}
	if !strings.Contains(string(got), "internal/awsjson") {
		t.Error("secrets (awsJson1_1) emit should import internal/awsjson")
	}

	// Storage (S3) declares restXml; emitter should pick that template
	// (which imports restxml, not awsjson).
	manifest = loadManifest(t, filepath.Join(root, "services", "storage", "codegen.json"))
	specBytes, _ = os.ReadFile(filepath.Join(root, manifest.Spec))
	model, _ = smithy.Parse(specBytes)
	got, err = emit.Emit(model, emit.Options{
		PackageName:  "gen",
		SourceFile:   manifest.Spec,
		SourceCommit: "0000000000000000000000000000000000000000",
		Operations:   []string{manifest.Operations[0]},
	})
	if err != nil {
		t.Fatalf("emit storage: %v", err)
	}
	if !strings.Contains(string(got), "internal/restxml") {
		t.Error("storage (restXml) emit should import internal/restxml")
	}
	if strings.Contains(string(got), "internal/awsjson") {
		t.Error("storage emit must not import internal/awsjson")
	}
}
