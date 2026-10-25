package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
)

func TestCheckFuncNames(t *testing.T) {
	process := func(expressions ...*model.Expression) []model.DeploymentProcess {
		m := make(map[string]*model.Expression)
		for _, e := range expressions {
			m[e.ElementId] = e
		}
		return []model.DeploymentProcess{{Expressions: m}}
	}

	if err := checkFuncNames(process(
		&model.Expression{ElementId: "Gateway_1", ConditionFunc: "gateway1"},
		&model.Expression{ElementId: "MyMethod", MethodFunc: "MyMethod"},
	), nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err := checkFuncNames(process(&model.Expression{ElementId: "Deployment", MethodFunc: "Deployment"}), nil)
	if err == nil || !strings.Contains(err.Error(), "занято") {
		t.Errorf("Deployment: err = %v", err)
	}

	err = checkFuncNames(process(
		&model.Expression{ElementId: "my_method", MethodFunc: "MyMethod"},
		&model.Expression{ElementId: "MyMethod", MethodFunc: "MyMethod"},
	), nil)
	if err == nil || !strings.Contains(err.Error(), "одно имя") {
		t.Errorf("collision: err = %v", err)
	}
}

func TestRemoveStaleProcessDirs(t *testing.T) {
	outputPath := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(outputPath, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("bpm-gen.go", generatedHeader+"\n\npackage bpm_gen\n")
	write("alive/flow-gen.go", generatedHeader+"\n\npackage alive\n")
	write("removed/flow-gen.go", generatedHeader+"\n\npackage removed\n")
	write("removed/queue-gen.go", generatedHeader+"\n\npackage removed\n")
	write("handwritten/flow-gen.go", generatedHeader+"\n\npackage handwritten\n")
	write("handwritten/method_my_method.go", "package handwritten\n")

	if err := removeStaleProcessDirs(outputPath, map[string]bool{"alive": true}); err != nil {
		t.Fatal(err)
	}

	exists := func(path string) bool {
		_, err := os.Stat(filepath.Join(outputPath, path))
		return err == nil
	}
	for path, want := range map[string]bool{
		"bpm-gen.go":                      true,
		"alive/flow-gen.go":               true,
		"removed":                         false,
		"handwritten/flow-gen.go":         false,
		"handwritten/method_my_method.go": true,
	} {
		if got := exists(path); got != want {
			t.Errorf("%s: exists = %v, want %v", path, got, want)
		}
	}
}
