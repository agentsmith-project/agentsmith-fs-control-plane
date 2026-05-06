package workerapp

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerAppImportBoundaries(t *testing.T) {
	allowed := map[string]bool{
		"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespacebindingexec": true,
		"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/mountbindingexec":     true,
		"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner":            true,
		"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoexec":             true,
		"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery":        true,
	}
	forbidden := []string{
		"/internal/api",
		"/internal/operationexec",
		"/cmd",
		"/jvs",
		"/webdav",
		"/mount",
		"/storage",
		"namespace_volume",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read workerapp package dir: %v", err)
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
			if allowed[importPath] {
				continue
			}
			for _, fragment := range forbidden {
				if strings.Contains(importPath, fragment) {
					t.Fatalf("workerapp import boundary violation: %s imports %q matching %q", name, importPath, fragment)
				}
			}
		}
	}
}
