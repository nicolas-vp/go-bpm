package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// TestBusinessRuleTask гоняет сгенерированный процесс TestDecision (process/testBusinessRule.bpmn):
// задача бизнес-правила вызывает решение Decision_1 из process/businessRule.dmn и пишет результат в c.
// End event процесса заменён задачей probe, чтобы увидеть переменные после решения
func TestBusinessRuleTask(t *testing.T) {
	const processName, end = "TestDecision", "Event_1jeqrbv"
	flow := generatedProcesses[processName].Flows[processName]
	if flow.Expressions[end] == nil {
		t.Fatalf("process %s: no element %s", processName, end)
	}
	flow.Expressions = maps.Clone(flow.Expressions)
	flow.Expressions[end] = &bpm.Expression{ElementId: end, ActivateType: bpm.ActivateTypeQueue, TaskType: "probe"}

	tests := []struct {
		variables map[string]any
		want      string
	}{
		{map[string]any{"a": "foo", "b": "bar"}, "correct foobar"},
		// правила пересекаются: берётся первое подошедшее
		{map[string]any{"a": "some", "b": "some"}, "some a"},
		{map[string]any{"a": "foo", "b": "some"}, "some b"},
		{map[string]any{"a": "foo"}, "default"},
		{nil, "default"},
	}
	for _, test := range tests {
		s, q := newTestService(t, map[string]bpm.DeploymentProcess{processName: flow}, []bpm.Queue{{TypeID: "probe"}})
		startAndActivate(t, s, q, nil, processName, test.variables)
		var message instanceMessage
		if err := json.Unmarshal(q.pop(t).Payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.Variables["c"] != test.want {
			t.Errorf("%v: c = %v, want %q", test.variables, message.Variables["c"], test.want)
		}
	}
}

// fakeRulesTx запоминает строки, вставленные в bpm_rules_history
type fakeRulesTx struct {
	queue.Tx
	t       *testing.T
	inserts [][]any
}

func (tx *fakeRulesTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if query != insertRulesHistoryQuery {
		tx.t.Fatalf("unexpected exec %q", query)
	}
	tx.inserts = append(tx.inserts, args)
	return pgconn.CommandTag{}, nil
}

// TestRulesHistory: сгенерированный метод передаёт сработавшие правила через bpm.RecordDecision,
// движок пишет их по флагам RulesHistory, и только если метод завершился успешно
func TestRulesHistory(t *testing.T) {
	flow := generatedProcesses["TestDecision"].Flows["TestDecision"]
	on := func() bool { return true }

	s, q := newTestService(t, map[string]bpm.DeploymentProcess{"TestDecision": flow}, nil)
	var out bytes.Buffer
	s.historyOut = &out
	s.rulesHistory = HistoryConfig{ToLog: on, ToDB: on}
	tx := &fakeRulesTx{t: t}
	startAndActivate(t, s, q, tx, "TestDecision", map[string]any{"a": "some", "b": "x"})

	// в TestDecision две задачи бизнес-правил подряд, обе выполняются в шаге старта
	if len(tx.inserts) != 2 {
		t.Fatalf("inserts = %v, want two rows", tx.inserts)
	}
	row := tx.inserts[0]
	if row[2] != "Activity_0xzljrt" || row[3] != "Decision_1" || row[4] != `["DecisionRule_1a2n073"]` || row[5] != `"some a"` {
		t.Errorf("row = %v", row)
	}
	if row := tx.inserts[1]; row[2] != "Activity_1mrup0q" || row[3] != "SubDecision" {
		t.Errorf("second row = %v", row)
	}
	if line := out.String(); !strings.Contains(line, `rules history: process="TestDecision"`) || !strings.Contains(line, `rules=["DecisionRule_1a2n073"]`) {
		t.Errorf("log = %q", line)
	}

	// флаги выключены - ничего не пишется
	s.rulesHistory = HistoryConfig{}
	out.Reset()
	startAndActivate(t, s, q, &fakeRulesTx{t: t}, "TestDecision", nil)
	if out.Len() != 0 {
		t.Errorf("log with disabled flags = %q", out.String())
	}

	// метод упал после вызова решения - правила не пишутся
	failing := &bpm.Expression{ElementId: "rule", ActivateType: bpm.ActivateTypeMethod,
		Method: func(ctx context.Context, variables map[string]any) (map[string]any, error) {
			if err := bpm.RecordDecision(ctx, bpm.DecisionResult{DecisionId: "D", RuleIds: []string{"r"}}); err != nil {
				return nil, err
			}
			return nil, errors.New("boom")
		}}
	s, _ = newTestService(t, map[string]bpm.DeploymentProcess{"f": {StartElement: "rule", Expressions: map[string]*bpm.Expression{"rule": failing}}}, nil)
	s.rulesHistory = HistoryConfig{ToDB: on}
	failTx := &fakeRulesTx{t: t}
	if err := s.moveNext(context.Background(), failTx, &instanceMessage{ProcessName: "f"}, "rule", moveActivate); err == nil {
		t.Fatal("want method error")
	}
	if len(failTx.inserts) != 0 {
		t.Errorf("failed method: inserts = %v", failTx.inserts)
	}
}
