package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
)

func TestMergeQueues(t *testing.T) {
	// doSomeAsync - в двух процессах (или дважды в одном)
	queues := mergeQueues([]model.Queue{
		{TypeID: "payment.charge", Retries: 3},
		{TypeID: "doSomeAsync", Retries: 1},
		{TypeID: "doSomeAsync", Retries: 5},
	})
	want := []model.Queue{
		{TypeID: "doSomeAsync", Retries: 5, HandlerFunc: "DoSomeAsync"},
		{TypeID: "payment.charge", Retries: 3, HandlerFunc: "PaymentCharge"},
	}
	if len(queues) != len(want) || queues[0] != want[0] || queues[1] != want[1] {
		t.Errorf("queues = %v, want %v", queues, want)
	}
}

func TestPrepareHandlerFiles(t *testing.T) {
	outputPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputPath, handlersDir), 0755); err != nil {
		t.Fatal(err)
	}
	handwritten := filepath.Join(outputPath, handlersDir, "make_smev.go")
	if err := os.WriteFile(handwritten, []byte("package handlers\n\n// руками\n"), 0644); err != nil {
		t.Fatal(err)
	}

	imports := dataImports{DataModelImport: "example.com/svc/gen/model", ServicesImport: "example.com/svc/gen/services"}
	if err := prepareHandlerFiles(mergeQueues([]model.Queue{{TypeID: "doSomeAsync"}, {TypeID: "makeSmev"}}), outputPath, imports); err != nil {
		t.Fatal(err)
	}
	stub, err := os.ReadFile(filepath.Join(outputPath, handlersDir, "do_some_async.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"package handlers",
		`"example.com/svc/gen/model"`,
		`"example.com/svc/gen/services"`,
		"func DoSomeAsync(ctx context.Context, svc *services.Services, job bpm.Job, data *model.DataModel) error",
	} {
		if !strings.Contains(string(stub), part) {
			t.Errorf("заглушка не содержит %q:\n%s", part, stub)
		}
	}
	if content, _ := os.ReadFile(handwritten); string(content) != "package handlers\n\n// руками\n" {
		t.Errorf("рукописный обработчик перезаписан:\n%s", content)
	}

	err = prepareHandlerFiles(mergeQueues([]model.Queue{{TypeID: "make-smev"}, {TypeID: "makeSmev"}}), outputPath, imports)
	if err == nil || !strings.Contains(err.Error(), "одно имя") {
		t.Errorf("collision: err = %v", err)
	}
}
