package codegen

import (
	"slices"
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
)

func validateDefinitions(body string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="P">` + body + `</bpmn:process>
</bpmn:definitions>`
}

// TestLinkFlowsFromSequenceFlows - BPMN, написанный руками, без <bpmn:incoming>/<bpmn:outgoing>: переходы
// восстанавливаются по sourceRef и targetRef, в том числе число веток слияния
func TestLinkFlowsFromSequenceFlows(t *testing.T) {
	expressions, err := parseEvents(t, validateDefinitions(`
    <bpmn:startEvent id="start" />
    <bpmn:parallelGateway id="fork" />
    <bpmn:task id="a" />
    <bpmn:task id="b" />
    <bpmn:parallelGateway id="join" />
    <bpmn:exclusiveGateway id="check" default="no" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="fork" />
    <bpmn:sequenceFlow id="f2" sourceRef="fork" targetRef="a" />
    <bpmn:sequenceFlow id="f3" sourceRef="fork" targetRef="b" />
    <bpmn:sequenceFlow id="f4" sourceRef="a" targetRef="join" />
    <bpmn:sequenceFlow id="f5" sourceRef="b" targetRef="join" />
    <bpmn:sequenceFlow id="f6" sourceRef="join" targetRef="check" />
    <bpmn:sequenceFlow id="yes" sourceRef="check" targetRef="end">
      <bpmn:conditionExpression>=done</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="no" sourceRef="check" targetRef="end" />`))
	if err != nil {
		t.Fatal(err)
	}
	if start := expressions["start"]; !slices.Equal(start.NextElements, []string{"f1"}) {
		t.Errorf("start = %+v, want next f1", start)
	}
	if fork := expressions["fork"]; fork.ActivateType != bpm.ActivateTypeFlow || !slices.Equal(fork.NextElements, []string{"f2", "f3"}) {
		t.Errorf("fork = %+v, want Flow to f2, f3", fork)
	}
	if join := expressions["join"]; join.ActivateType != bpm.ActivateTypeSyncQueue || join.ParallelSyncQuantity != 2 {
		t.Errorf("join = %+v, want SyncQueue of 2", join)
	}
	check := expressions["check"]
	if check.CompleteType != bpm.CompleteTypeScript || check.Condition == nil || check.Condition.DefaultFlow != "no" ||
		len(check.Condition.Branches) != 1 || check.Condition.Branches[0].FlowId != "yes" {
		t.Errorf("check = %+v, condition %+v", check, check.Condition)
	}
}

// TestParallelGatewayJoinAndFork - шлюз, который сливает ветки и сразу разветвляет: токен уходит во все выходы,
// а не в выбор по условиям, которых нет
func TestParallelGatewayJoinAndFork(t *testing.T) {
	expressions, err := parseEvents(t, validateDefinitions(`
    <bpmn:startEvent id="start" />
    <bpmn:parallelGateway id="fork" />
    <bpmn:parallelGateway id="both" />
    <bpmn:endEvent id="end1" />
    <bpmn:endEvent id="end2" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="fork" />
    <bpmn:sequenceFlow id="f2" sourceRef="fork" targetRef="both" />
    <bpmn:sequenceFlow id="f3" sourceRef="fork" targetRef="both" />
    <bpmn:sequenceFlow id="f4" sourceRef="both" targetRef="end1" />
    <bpmn:sequenceFlow id="f5" sourceRef="both" targetRef="end2" />`))
	if err != nil {
		t.Fatal(err)
	}
	both := expressions["both"]
	if both.ActivateType != bpm.ActivateTypeSyncQueue || both.ParallelSyncQuantity != 2 ||
		both.CompleteType != bpm.CompleteTypeStraight || !slices.Equal(both.NextElements, []string{"f4", "f5"}) {
		t.Errorf("both = %+v, want SyncQueue of 2 straight to f4, f5", both)
	}
}

// TestUnsupportedElements - то, что раньше генерировалось молча и падало в рантайме, теперь ошибка генерации
func TestUnsupportedElements(t *testing.T) {
	const straight = `
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="end"><bpmn:incoming>f1</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end" />`
	for _, tc := range []struct {
		name, body, want string
	}{
		{"inclusive gateway", `
    <bpmn:startEvent id="start" />
    <bpmn:inclusiveGateway id="X" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="X" />`, "inclusiveGateway X не поддерживается"},
		{"manual task", `<bpmn:manualTask id="M" />` + straight, "manualTask M не поддерживается"},
		{"intermediate throw event", `<bpmn:intermediateThrowEvent id="T" />` + straight, "intermediateThrowEvent T"},
		{"flow to unknown element", straight + `<bpmn:sequenceFlow id="f2" sourceRef="start" targetRef="ghost" />`,
			`sequence flow f2: элемент "ghost" не найден`},
		{"no start event", `<bpmn:endEvent id="end" />`, "нет start event"},
		{"two start events", `<bpmn:startEvent id="s2" />` + straight, "несколько start event (s2, start)"},
		{"timer start event", `
    <bpmn:startEvent id="start"><bpmn:timerEventDefinition><bpmn:timeCycle>R/PT1H</bpmn:timeCycle></bpmn:timerEventDefinition></bpmn:startEvent>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end" />`, "start event start: timerEventDefinition"},
		{"terminate end event", `
    <bpmn:startEvent id="start" />
    <bpmn:endEvent id="end"><bpmn:terminateEventDefinition /></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end" />`, "end event end: terminateEventDefinition"},
		{"multi-instance task", `
    <bpmn:startEvent id="start" />
    <bpmn:task id="each"><bpmn:multiInstanceLoopCharacteristics /></bpmn:task>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="each" />
    <bpmn:sequenceFlow id="f2" sourceRef="each" targetRef="end" />`, "task each: циклы и multi-instance"},
		{"nested sub-process", `
    <bpmn:startEvent id="start" />
    <bpmn:subProcess id="outer">
      <bpmn:startEvent id="s1" />
      <bpmn:subProcess id="inner"><bpmn:startEvent id="s2" /></bpmn:subProcess>
    </bpmn:subProcess>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="outer" />`, "вложенный подпроцесс inner"},
		{"outgoing differs from sequence flows", `
    <bpmn:startEvent id="start"><bpmn:outgoing>f9</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end" />`, "переходы в <bpmn:outgoing> (f9) не совпадают с sequence flow, у которых sourceRef = start (f1)"},
		{"two outgoing from task", `
    <bpmn:startEvent id="start" />
    <bpmn:task id="t" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="t" />
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="end" />
    <bpmn:sequenceFlow id="f3" sourceRef="t" targetRef="end" />`, "task t: несколько исходящих переходов (f2, f3)"},
		{"condition outside exclusive gateway", `
    <bpmn:startEvent id="start" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"><bpmn:conditionExpression>=ok</bpmn:conditionExpression></bpmn:sequenceFlow>`,
			"start event start: условие на переходе f1 проверяет только exclusive gateway"},
		{"branch without condition", `
    <bpmn:startEvent id="start" />
    <bpmn:exclusiveGateway id="g" default="f3" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="g" />
    <bpmn:sequenceFlow id="f2" sourceRef="g" targetRef="end" />
    <bpmn:sequenceFlow id="f3" sourceRef="g" targetRef="end" />
    <bpmn:sequenceFlow id="f4" sourceRef="g" targetRef="end"><bpmn:conditionExpression>=ok</bpmn:conditionExpression></bpmn:sequenceFlow>`,
			"exclusiveGateway g: у перехода f2 нет условия, и он не default flow"},
		{"default flow of another element", straight + `<bpmn:exclusiveGateway id="g" default="f1" />`,
			"exclusiveGateway g: default flow f1 не выходит из шлюза"},
	} {
		_, err := parseEvents(t, validateDefinitions(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestIgnoredElements - документация, лейны и аннотации не исполняются и не мешают генерации
func TestIgnoredElements(t *testing.T) {
	_, err := parseEvents(t, validateDefinitions(`
    <bpmn:documentation>Процесс</bpmn:documentation>
    <bpmn:laneSet id="lanes"><bpmn:lane id="lane" /></bpmn:laneSet>
    <bpmn:textAnnotation id="note"><bpmn:text>Заметка</bpmn:text></bpmn:textAnnotation>
    <bpmn:association id="link" sourceRef="start" targetRef="note" />
    <bpmn:startEvent id="start" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end" />`))
	if err != nil {
		t.Fatal(err)
	}
}
