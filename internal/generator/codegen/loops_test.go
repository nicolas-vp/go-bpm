package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
)

const loopsDefinitions = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <bpmn:process id="Loops">
    <bpmn:startEvent id="start" />
    <bpmn:task id="each">
      <bpmn:multiInstanceLoopCharacteristics isSequential="true">
        <bpmn:loopCardinality xsi:type="bpmn:tFormalExpression">3</bpmn:loopCardinality>
      </bpmn:multiInstanceLoopCharacteristics>
    </bpmn:task>
    <bpmn:serviceTask id="charge">
      <bpmn:extensionElements><zeebe:taskDefinition type="charge" /></bpmn:extensionElements>
      <bpmn:multiInstanceLoopCharacteristics isSequential="true">
        <bpmn:completionCondition xsi:type="bpmn:tFormalExpression">=approved or a &gt;= 5</bpmn:completionCondition>
      </bpmn:multiInstanceLoopCharacteristics>
    </bpmn:serviceTask>
    <bpmn:boundaryEvent id="failed" attachedToRef="charge"><bpmn:errorEventDefinition /></bpmn:boundaryEvent>
    <bpmn:subProcess id="sub">
      <bpmn:multiInstanceLoopCharacteristics isSequential="true">
        <bpmn:loopCardinality>= 2</bpmn:loopCardinality>
        <bpmn:completionCondition>approved</bpmn:completionCondition>
      </bpmn:multiInstanceLoopCharacteristics>
      <bpmn:startEvent id="subStart" />
      <bpmn:endEvent id="subEnd" />
      <bpmn:sequenceFlow id="s1" sourceRef="subStart" targetRef="subEnd" />
    </bpmn:subProcess>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="each" />
    <bpmn:sequenceFlow id="f2" sourceRef="each" targetRef="charge" />
    <bpmn:sequenceFlow id="f3" sourceRef="charge" targetRef="sub" />
    <bpmn:sequenceFlow id="f4" sourceRef="sub" targetRef="end" />
    <bpmn:sequenceFlow id="f5" sourceRef="failed" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`

func TestLoops(t *testing.T) {
	expressions, err := parseEvents(t, loopsDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]model.Loop{
		"each":   {Cardinality: 3},
		"charge": {CompletionCondition: "approved or a >= 5", ConditionFunc: "chargeCompleted"},
		"sub":    {Cardinality: 2, CompletionCondition: "approved", ConditionFunc: "subCompleted"},
	} {
		if got := expressions[id].Loop; got == nil || *got != want {
			t.Errorf("%s: loop = %+v, want %+v", id, got, want)
		}
	}
	if expressions["end"].Loop != nil {
		t.Error("end: цикл без multi-instance")
	}
}

func TestUnsupportedLoops(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, wantErr string }{
		"parallel": {`<bpmn:multiInstanceLoopCharacteristics isSequential="true">
        <bpmn:loopCardinality xsi`, `<bpmn:multiInstanceLoopCharacteristics>
        <bpmn:loopCardinality xsi`, "each: параллельный multi-instance"},
		"cardinality expression": {">3<", ">${count}<", `loopCardinality "${count}"`},
		"zero cardinality":       {">3<", ">0<", "больше нуля"},
		"empty": {`<bpmn:loopCardinality xsi:type="bpmn:tFormalExpression">3</bpmn:loopCardinality>`, ``,
			"нужно число выполнений (loopCardinality) или условие завершения"},
		"zeebe collection": {`<bpmn:loopCardinality xsi:type="bpmn:tFormalExpression">3</bpmn:loopCardinality>`,
			`<bpmn:extensionElements><zeebe:loopCharacteristics inputCollection="=items" inputElement="item" /></bpmn:extensionElements>`,
			"zeebe:loopCharacteristics inputCollection"},
		"timer boundary": {`<bpmn:errorEventDefinition />`, `<bpmn:timerEventDefinition><bpmn:timeDuration>PT1M</bpmn:timeDuration></bpmn:timerEventDefinition>`,
			"граничный таймер у элемента charge с multi-instance"},
	} {
		source := strings.Replace(loopsDefinitions, tc.from, tc.to, 1)
		if source == loopsDefinitions {
			t.Fatalf("%s: %q not found in BPMN", name, tc.from)
		}
		if _, err := parseEvents(t, source); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
		}
	}
}

func TestPrepareLoopFiles(t *testing.T) {
	m := loadTestModel(t, testModelSource)
	processes := []model.DeploymentProcess{{Expressions: map[string]*model.Expression{
		"charge": {ElementId: "charge", Loop: &model.Loop{CompletionCondition: "approved or a >= 5", ConditionFunc: "chargeCompleted"}},
		"each":   {ElementId: "each", Loop: &model.Loop{Cardinality: 3}},
		"broken": {ElementId: "broken", Loop: &model.Loop{CompletionCondition: "loopCounter > 2", ConditionFunc: "brokenCompleted"}},
	}}}
	dir := t.TempDir()
	gaps := newModelGaps()
	imports := dataImports{DataModelImport: "example.com/svc/gen/model"}
	if err := prepareLoopFiles(processes, "P", dir, "p", imports, m, gaps); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "loop_charge.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"func chargeCompleted(data *model.DataModel) bool",
		"// в процессе: approved or a >= 5",
		"return data.Approved || data.A >= 5",
	} {
		if !strings.Contains(string(content), part) {
			t.Errorf("условие не содержит %q:\n%s", part, content)
		}
	}
	// у цикла без условия файла нет, у условия с недостающим полем - тоже, поле попадает в подсказку
	for _, name := range []string{"loop_each.go", "loop_broken.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s записан", name)
		}
	}
	if err := gaps.err(); err == nil || !strings.Contains(err.Error(), "LoopCounter int `json:\"loopCounter\"`") ||
		!strings.Contains(err.Error(), "процесс P: условие завершения цикла broken") {
		t.Errorf("gaps: %v", err)
	}
}
