package namespacebindingexec

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamespaceVolumeBindingExecImportBoundaries(t *testing.T) {
	forbidden := []string{
		"/internal/api",
		"/internal/operationexec",
		"/cmd",
		"/jvs",
		"/webdav",
		"/mount",
		"/storage",
		"/postgres",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read namespacebindingexec package dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Clean(name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			importPath := strings.Trim(spec.Path.Value, `"`)
			for _, fragment := range forbidden {
				if strings.Contains(importPath, fragment) {
					t.Fatalf("namespacebindingexec import boundary violation: %s imports %q matching %q", name, importPath, fragment)
				}
			}
		}
	}
}
