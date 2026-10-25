package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

func TestPrepareDataFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/svc\n\ngo 1.26.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "gen")

	imports, err := prepareDataFiles(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	want := dataImports{DataModelImport: "example.com/svc/gen/model", ServicesImport: "example.com/svc/gen/services"}
	if imports != want {
		t.Errorf("imports = %+v, want %+v", imports, want)
	}
	for path, part := range map[string]string{
		filepath.Join(outputPath, modelDir, "data_model.go"):  "type DataModel struct",
		filepath.Join(outputPath, servicesDir, "services.go"): "type Services struct",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), part) || strings.HasPrefix(string(content), generatedHeader) {
			t.Errorf("%s: нет %q или есть заголовок сгенерированного файла:\n%s", path, part, content)
		}
	}

	// модель дописана руками и разнесена по файлам - генератор её не трогает
	modelPath := filepath.Join(outputPath, modelDir, "data_model.go")
	if err := os.Remove(modelPath); err != nil {
		t.Fatal(err)
	}
	handwritten := filepath.Join(outputPath, modelDir, "order.go")
	if err := os.WriteFile(handwritten, []byte("package model\n\ntype DataModel struct{ Amount float64 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareDataFiles(outputPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(modelPath); err == nil {
		t.Errorf("%s создан заново рядом с рукописной моделью", modelPath)
	}
}

func TestPrepareMethodStubFiles(t *testing.T) {
	dir := t.TempDir()
	process := &bpmn.Process{Tasks: []bpmn.Task{{Id: "Activity_1", Name: "Проверить"}}}
	imports := dataImports{DataModelImport: "example.com/svc/gen/model", ServicesImport: "example.com/svc/gen/services"}
	if err := prepareMethodStubFiles(process, dir, "my_process", imports); err != nil {
		t.Fatal(err)
	}
	stub, err := os.ReadFile(filepath.Join(dir, "method_activity_1.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		`"example.com/svc/gen/model"`,
		`"example.com/svc/gen/services"`,
		"func Activity1(ctx context.Context, svc *services.Services, data *model.DataModel) error",
	} {
		if !strings.Contains(string(stub), part) {
			t.Errorf("заглушка не содержит %q:\n%s", part, stub)
		}
	}
}
