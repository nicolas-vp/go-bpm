package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
)

// loopDeployment: start -> work (цикл loop) -> after (service task) -> end; у work граничное событие ошибки failed -> end
func loopDeployment(work *bpm.Expression) map[string]bpm.DeploymentProcess {
	work.ElementId = "work"
	work.NextElements = []string{"after"}
	work.Boundaries = []bpm.Boundary{{ElementId: "failed", Type: bpm.BoundaryTypeError}}
	return map[string]bpm.DeploymentProcess{
		"l": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start":  {ElementId: "start", NextElements: []string{"work"}},
			"work":   work,
			"after":  {ElementId: "after", ActivateType: bpm.ActivateTypeQueue, TaskType: "after", NextElements: []string{"end"}},
			"end":    {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
			"failed": {ElementId: "failed", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"end"}},
		}},
	}
}

// runLoopSteps выполняет системные задачи следующего выполнения цикла, пока они есть
func runLoopSteps(t *testing.T, s *Engine, q *fakeQueue) {
	t.Helper()
	for len(q.pushed) > 0 && q.pushed[0].Kind == s.systemKind {
		var st systemTask
		if err := json.Unmarshal(q.pop(t).Payload, &st); err != nil {
			t.Fatal(err)
		}
		if st.Type != systemLoop {
			t.Fatalf("system task %q, want %q", st.Type, systemLoop)
		}
		if err := s.moveNext(context.Background(), fakeExecTx{}, st.Message, st.Message.CurrentPosition, moveActivate); err != nil {
			t.Fatal(err)
		}
	}
}

// popAfter достаёт задачу after, к которой токен приходит после цикла: состояния цикла в нём уже нет
func popAfter(t *testing.T, s *Engine, q *fakeQueue) *instanceMessage {
	t.Helper()
	task := q.popKind(t, s.taskKinds["after"])
	var message instanceMessage
	if err := json.Unmarshal(task.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if _, found := message.Variables[bpm.LoopCounterVariable]; found || message.Loops != nil {
		t.Errorf("после цикла: variables %v, loops %v", message.Variables, message.Loops)
	}
	return &message
}

// seenCounters - метод, который запоминает номера выполнений
func seenCounters(seen *[]float64) bpm.MethodFunc {
	return func(_ context.Context, variables map[string]any) (map[string]any, error) {
		// номер выполнения проходит через JSON очереди, поэтому сравниваем как число JSON
		raw, _ := json.Marshal(variables[bpm.LoopCounterVariable])
		var counter float64
		_ = json.Unmarshal(raw, &counter)
		*seen = append(*seen, counter)
		return variables, nil
	}
}

func TestLoopCardinalityMethod(t *testing.T) {
	var seen []float64
	s, q := newTestService(t, loopDeployment(&bpm.Expression{
		ActivateType: bpm.ActivateTypeMethod,
		Method:       seenCounters(&seen),
		Loop:         &bpm.Loop{Cardinality: 3},
	}), []bpm.Queue{{TypeID: "after"}})

	startAndActivate(t, s, q, fakeExecTx{}, "l", map[string]any{"a": 1})
	// первое выполнение - в шаге старта, следующее - отдельной задачей
	if len(seen) != 1 || len(q.pushed) != 1 || q.pushed[0].Kind != s.systemKind {
		t.Fatalf("после старта: seen %v, queue %+v", seen, q.pushed)
	}
	runLoopSteps(t, s, q)
	if len(seen) != 3 || seen[0] != 1 || seen[1] != 2 || seen[2] != 3 {
		t.Errorf("seen = %v, want [1 2 3]", seen)
	}
	if message := popAfter(t, s, q); message.Variables["a"] != float64(1) {
		t.Errorf("variables = %v", message.Variables)
	}
}

// TestLoopCompletionServiceTask - service task повторяется, пока условие завершения не выполнилось;
// условие видит номер выполнения
func TestLoopCompletionServiceTask(t *testing.T) {
	var counters []any
	s, q := newTestService(t, loopDeployment(&bpm.Expression{
		ActivateType: bpm.ActivateTypeQueue,
		TaskType:     "work",
		Loop: &bpm.Loop{CompletionCondition: func(variables map[string]any) (bool, error) {
			counters = append(counters, variables[bpm.LoopCounterVariable])
			return variables["done"] == true, nil
		}},
	}), []bpm.Queue{{TypeID: "work"}, {TypeID: "after"}})

	startAndActivate(t, s, q, fakeExecTx{}, "l", nil)
	for i := 1; i <= 3; i++ {
		completeTaskWith(t, s, q, fakeExecTx{}, "work", func(message *instanceMessage) {
			if got := message.Variables[bpm.LoopCounterVariable]; got != float64(i) {
				t.Errorf("выполнение %d: loopCounter = %v", i, got)
			}
			if i == 3 {
				message.Variables["done"] = true
			}
		})
	}
	if len(counters) != 3 || counters[2] != 3 {
		t.Errorf("условие видело loopCounter %v, want 1, 2, 3", counters)
	}
	if message := popAfter(t, s, q); message.Variables["done"] != true {
		t.Errorf("variables = %v", message.Variables)
	}
}

// TestLoopCardinalityAndCondition - цикл кончается тем, что наступит раньше: условием или числом выполнений
func TestLoopCardinalityAndCondition(t *testing.T) {
	var seen []float64
	s, q := newTestService(t, loopDeployment(&bpm.Expression{
		ActivateType: bpm.ActivateTypeMethod,
		Method:       seenCounters(&seen),
		Loop: &bpm.Loop{Cardinality: 5, CompletionCondition: func(variables map[string]any) (bool, error) {
			return variables[bpm.LoopCounterVariable] == 2, nil
		}},
	}), []bpm.Queue{{TypeID: "after"}})

	startAndActivate(t, s, q, fakeExecTx{}, "l", nil)
	runLoopSteps(t, s, q)
	if len(seen) != 2 {
		t.Errorf("seen = %v, want 2 выполнения", seen)
	}
	popAfter(t, s, q)
}

// TestLoopErrorBoundary - граничное событие ошибки прерывает весь цикл и убирает его состояние из токена
func TestLoopErrorBoundary(t *testing.T) {
	var seen []float64
	count := seenCounters(&seen)
	s, q := newTestService(t, loopDeployment(&bpm.Expression{
		ActivateType: bpm.ActivateTypeMethod,
		Method: func(ctx context.Context, variables map[string]any) (map[string]any, error) {
			if _, err := count(ctx, variables); err != nil {
				return nil, err
			}
			if len(seen) == 2 {
				return nil, bpm.NewError("STOP", "stop")
			}
			return variables, nil
		},
		Loop: &bpm.Loop{Cardinality: 5},
	}), []bpm.Queue{{TypeID: "after"}})
	out := captureHistory(s)

	startAndActivate(t, s, q, fakeExecTx{}, "l", nil)
	runLoopSteps(t, s, q)
	if len(seen) != 2 || len(q.pushed) != 0 {
		t.Fatalf("seen %v, queue %+v: цикл должен прерваться на втором выполнении", seen, q.pushed)
	}
	history := historyElements(out.String())
	if len(history) < 3 || history[len(history)-3] != "terminated work" || history[len(history)-1] != "completed end" {
		t.Errorf("history = %v", history)
	}
}

// TestLoopRestartsAfterLeave - элемент, в который токен вернулся после конца цикла, начинает цикл заново
func TestLoopRestartsAfterLeave(t *testing.T) {
	message := &instanceMessage{ProcessName: "l"}
	expression := &bpm.Expression{ElementId: "work", Loop: &bpm.Loop{Cardinality: 2}}
	for round := 0; round < 2; round++ {
		enterLoop(message, expression)
		if again, err := nextIteration(message, expression); err != nil || !again {
			t.Fatalf("round %d: первое выполнение: again %v, err %v", round, again, err)
		}
		enterLoop(message, expression)
		if message.Variables[bpm.LoopCounterVariable] != 2 {
			t.Fatalf("round %d: loopCounter = %v", round, message.Variables[bpm.LoopCounterVariable])
		}
		if again, err := nextIteration(message, expression); err != nil || again {
			t.Fatalf("round %d: второе выполнение: again %v, err %v", round, again, err)
		}
		if message.Loops != nil {
			t.Fatalf("round %d: loops = %v", round, message.Loops)
		}
	}
}

// captureHistory выводит историю движка в буфер
func captureHistory(s *Engine) *bytes.Buffer {
	out := &bytes.Buffer{}
	s.history = HistoryConfig{ToLog: func() bool { return true }}
	s.historyOut = out
	return out
}

// historyElements - история с экрана строками "<событие> <элемент>"
func historyElements(printed string) []string {
	var history []string
	for _, match := range historyElement.FindAllStringSubmatch(printed, -1) {
		history = append(history, match[1]+" "+match[2])
	}
	return history
}
