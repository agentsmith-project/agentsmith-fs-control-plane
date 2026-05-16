package repoexec

import (
	"reflect"
	"testing"
)

func TestJVSRunnerInterfaceIsDirectOnly(t *testing.T) {
	t.Parallel()

	runnerType := reflect.TypeOf((*JVSRunner)(nil)).Elem()
	want := map[string]bool{
		"DirectSave":    true,
		"DirectList":    true,
		"DirectRestore": true,
		"DirectStatus":  true,
		"DirectDoctor":  true,
	}
	if runnerType.NumMethod() != len(want) {
		t.Fatalf("JVSRunner method count = %d, want direct-only %d", runnerType.NumMethod(), len(want))
	}
	for i := 0; i < runnerType.NumMethod(); i++ {
		name := runnerType.Method(i).Name
		if !want[name] {
			t.Fatalf("JVSRunner exposes non-direct method %s", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Fatalf("JVSRunner missing direct method %s", name)
	}
}
