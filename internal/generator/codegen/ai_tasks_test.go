package codegen

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

// aiDefinitions - процесс с AI task, как её пишет моделер: gobpm:taskDefinition type="ai" и метка gobpm:aiTask
const aiDefinitions = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:gobpm="https://github.com/nicolas-vp/go-bpm/schema/1.0">
  <bpmn:process id="Ai">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="classify">
      <bpmn:extensionElements>
        <gobpm:taskDefinition type="ai" retries="3" />
        <gobpm:aiTask model="claude-sonnet-5-5" systemPrompt="Ты классифицируешь обращения" prompt="=request.text" resultVariable="category" />
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="classify" />
    <bpmn:sequenceFlow id="f2" sourceRef="classify" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`

func TestAiTask(t *testing.T) {
	expressions, err := parseEvents(t, aiDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	classify := expressions["classify"]
	want := bpm.AITask{Model: "claude-sonnet-5-5", SystemPrompt: "Ты классифицируешь обращения", Prompt: "${request.text}", ResultVariable: "category"}
	if classify.ActivateType != bpm.ActivateTypeQueue || classify.TaskType != "ai" || classify.AITask == nil || *classify.AITask != want {
		t.Errorf("classify = %+v, AITask %+v, want Queue ai %+v", classify, classify.AITask, want)
	}
}

func TestAiTaskQueue(t *testing.T) {
	// без gobpm:taskDefinition jobType - ai
	source := strings.Replace(aiDefinitions, `<gobpm:taskDefinition type="ai" retries="3" />`, ``, 1)
	var queues []model.Queue
	expressions, err := parseProcess(t, source, &queues)
	if err != nil {
		t.Fatal(err)
	}
	if expressions["classify"].TaskType != DefaultAiJobType || len(queues) != 1 || queues[0] != (model.Queue{TypeID: "ai", Retries: 1, AI: true}) {
		t.Errorf("TaskType %q, queues %+v", expressions["classify"].TaskType, queues)
	}
}

func TestAiTaskPrompts(t *testing.T) {
	for prompt, want := range map[string]string{
		"=request.text":              "${request.text}",
		"= text ":                    "${text}",
		"Обращение: ${request.text}": "Обращение: ${request.text}",
		"Без переменных, $ и {скобки}": "Без переменных, $ и {скобки}",
		"${ a.b } и ${c}": "${ a.b } и ${c}",
	} {
		got, err := promptValue("t", "prompt", prompt)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v, want %q", prompt, got, err, want)
		}
	}
	if got := promptVariables("${ a.b } и ${c}"); len(got) != 2 || got[0] != "a.b" || got[1] != "c" {
		t.Errorf("promptVariables = %v", got)
	}
}

func TestUnsupportedAiTasks(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, wantErr string }{
		"no prompt":         {`prompt="=request.text"`, ``, "не задан промпт"},
		"no result":         {`resultVariable="category"`, ``, "не задана переменная результата"},
		"bad result":        {`resultVariable="category"`, `resultVariable="a + b"`, "не имя переменной"},
		"feel expression":   {`prompt="=request.text"`, `prompt="=&quot;a&quot; + b"`, "выражение не поддерживается"},
		"bad placeholder":   {`prompt="=request.text"`, `prompt="Текст ${a + b}"`, "не переменная"},
		"zeebe namespace":   {`<gobpm:aiTask`, `<zeebe:aiTask xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"`, ""},
		"plain no job type": {`<gobpm:taskDefinition type="ai" retries="3" />`, `<gobpm:taskDefinition retries="3" />`, ""},
	} {
		source := strings.Replace(aiDefinitions, tc.from, tc.to, 1)
		if source == aiDefinitions {
			t.Fatalf("%s: %q not found in BPMN", name, tc.from)
		}
		expressions, err := parseEvents(t, source)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
			}
		case name == "zeebe namespace":
			// aiTask только в пространстве имён go-bpm: zeebe:aiTask - обычная service task с обработчиком ai
			if err != nil || expressions["classify"].AITask != nil {
				t.Errorf("%s: err = %v, AITask = %+v", name, err, expressions["classify"].AITask)
			}
		default:
			// AI task без type - jobType ai
			if err != nil || expressions["classify"].TaskType != DefaultAiJobType {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	}
}

func TestServiceTaskWithoutType(t *testing.T) {
	source := strings.Replace(aiDefinitions, `<gobpm:aiTask model="claude-sonnet-5-5" systemPrompt="Ты классифицируешь обращения" prompt="=request.text" resultVariable="category" />`, ``, 1)
	source = strings.Replace(source, `type="ai" `, ``, 1)
	if _, err := parseEvents(t, source); err == nil || !strings.Contains(err.Error(), "не задан тип задачи") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckAiTasks(t *testing.T) {
	m := loadTestModel(t, testModelSource+
		"\ntype Verdict struct {\n\tOk bool `json:\"ok\"`\n}\n")
	for name, tc := range map[string]struct {
		task bpm.AITask
		json bool
		gaps int
	}{
		"string":         {task: bpm.AITask{Prompt: "${name}", ResultVariable: "name"}},
		"pointer string": {task: bpm.AITask{Prompt: "x", ResultVariable: "reason"}},
		"nested string":  {task: bpm.AITask{Prompt: "x", ResultVariable: "client.account.number"}},
		"int":            {task: bpm.AITask{Prompt: "x", ResultVariable: "score"}, json: true},
		"struct":         {task: bpm.AITask{Prompt: "x", ResultVariable: "discount"}, json: true},
		"slice":          {task: bpm.AITask{Prompt: "x", ResultVariable: "tags"}, json: true},
		"no result":      {task: bpm.AITask{Prompt: "x", ResultVariable: "answer"}, gaps: 1},
		"no prompt var":  {task: bpm.AITask{SystemPrompt: "${rules}", Prompt: "${client.missing}", ResultVariable: "name"}, gaps: 1},
	} {
		task := tc.task
		gaps := newModelGaps()
		checkAiTasks([]model.DeploymentProcess{{Expressions: map[string]*model.Expression{"t": {ElementId: "t", AITask: &task}}}}, "P", m, gaps)
		if task.JSON != tc.json || gaps.len() != tc.gaps {
			t.Errorf("%s: JSON = %v, gaps %d (%v), want %v, %d", name, task.JSON, gaps.len(), gaps.err(), tc.json, tc.gaps)
		}
	}
}

// parseProcess разбирает первый процесс source и собирает его очереди в queues
func parseProcess(t *testing.T, source string, queues *[]model.Queue) (map[string]*model.Expression, error) {
	t.Helper()
	var definitions bpmn.Definitions
	if err := xml.Unmarshal([]byte(source), &definitions); err != nil {
		t.Fatal(err)
	}
	return ParseFromProcess(&definitions.Processes[0], queues, &definitions)
}
