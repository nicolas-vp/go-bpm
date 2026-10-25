package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
)

func TestPrepareConditionFiles(t *testing.T) {
	m := loadTestModel(t, testModelSource)
	processes := []model.DeploymentProcess{{Expressions: map[string]*model.Expression{
		"Gateway_1": {ElementId: "Gateway_1", ConditionFunc: "gateway1", Condition: &model.Condition{
			DefaultFlow: "Flow_default",
			Branches:    []model.ConditionBranch{{FlowId: "Flow_1", Expression: "a == 55 and client.account.number = \"x\""}},
		}},
		"Gateway_2": {ElementId: "Gateway_2", ConditionFunc: "gateway2", Condition: &model.Condition{
			Branches: []model.ConditionBranch{{FlowId: "Flow_2", Expression: "missing > 1"}},
		}},
	}}}
	dir := t.TempDir()
	gaps := newModelGaps()
	imports := dataImports{DataModelImport: "example.com/svc/gen/model"}
	if err := prepareConditionFiles(processes, "P", dir, "p", imports, m, gaps); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "cond_gateway_1.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		`"example.com/svc/gen/model"`,
		"func gateway1(data *model.DataModel) string",
		`// в процессе: a == 55 and client.account.number = "x"`,
		`if data.A == 55 && data.Client.Account.Number == "x" {`,
		`return "Flow_default"`,
	} {
		if !strings.Contains(string(content), part) {
			t.Errorf("условие не содержит %q:\n%s", part, content)
		}
	}
	// условие с недостающим полем не пишется, поле попадает в подсказку
	if _, err := os.Stat(filepath.Join(dir, "cond_gateway_2.go")); err == nil {
		t.Error("условие с недостающим полем записано")
	}
	if err := gaps.err(); err == nil || !strings.Contains(err.Error(), "Missing int `json:\"missing\"`") ||
		!strings.Contains(err.Error(), "процесс P: условие Gateway_2") {
		t.Errorf("gaps: %v", err)
	}
}
