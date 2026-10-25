package engine

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

func TestResolveAssignment(t *testing.T) {
	variables := map[string]any{
		"initiator": "alice",
		"empty":     "",
		"groups":    []any{"managers", "admins"},
		"mixed":     []any{"auditors", 5.0},
		"client":    map[string]any{"manager": "bob"},
		"amount":    float64(5),
		"nothing":   nil,
	}
	for _, tc := range []struct {
		values       []string
		want         []string
		wantProblems string
	}{
		{values: []string{"boss"}, want: []string{"boss"}},
		{values: []string{""}, want: []string{}},
		{values: []string{"${initiator}", "${client.manager}"}, want: []string{"alice", "bob"}},
		{values: []string{"auditors", "${groups}"}, want: []string{"auditors", "managers", "admins"}},
		{values: []string{"${empty}", "${nothing}"}, want: []string{}},
		// не вычислилось - значение пропускается, остальные остаются
		{values: []string{"boss", "${missing}"}, want: []string{"boss"}, wantProblems: `x: variable "missing" not found`},
		{values: []string{"${client.missing}"}, want: []string{}, wantProblems: "not found"},
		{values: []string{"${amount}"}, want: []string{}, wantProblems: "string or string list expected"},
		{values: []string{"${mixed}"}, want: []string{"auditors"}, wantProblems: "string list expected"},
	} {
		got, problems := resolveAssignment(variables, "x", tc.values)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%v = %v, want %v", tc.values, got, tc.want)
		}
		joined := strings.Join(problems, "; ")
		if (tc.wantProblems == "") != (joined == "") || !strings.Contains(joined, tc.wantProblems) {
			t.Errorf("%v: problems = %q, want %q", tc.values, joined, tc.wantProblems)
		}
	}
}

// fakeUserTaskTx запоминает аргументы вставки задачи пользователя
type fakeUserTaskTx struct {
	queue.Tx
	inserted [][]any
}

func (tx *fakeUserTaskTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if query == insertUserTaskQuery {
		tx.inserted = append(tx.inserted, args)
	}
	return pgconn.CommandTag{}, nil
}

func TestActivateUserTask(t *testing.T) {
	deployment := map[string]bpm.DeploymentProcess{
		"main": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"approve"}},
			"approve": {ElementId: "approve", ActivateType: bpm.ActivateTypeUserTask, NextElements: []string{"end"},
				UserTask: &bpm.UserTask{Name: "Согласовать", Assignee: "${initiator}", CandidateGroups: []string{"managers"}, FormKey: "form"}},
			"end": {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	s, q := newTestService(t, deployment, nil)
	tx := &fakeUserTaskTx{}
	startAndActivate(t, s, q, tx, "main", map[string]any{"initiator": "alice"}, WithBusinessKey("order-1"))
	if len(tx.inserted) != 1 || len(q.pushed) != 0 {
		t.Fatalf("inserted %v, pushed %v: token must wait in the user task", tx.inserted, q.pushed)
	}
	args := tx.inserted[0]
	if args[3] != "approve" || args[4] != "Согласовать" || args[5] != "order-1" || args[6] != "form" || args[7] != "alice" ||
		!slices.Equal(args[8].([]string), []string{}) || !slices.Equal(args[9].([]string), []string{"managers"}) {
		t.Errorf("insert args = %v", args)
	}
	var message instanceMessage
	if err := json.Unmarshal([]byte(args[10].(string)), &message); err != nil {
		t.Fatal(err)
	}
	if message.CurrentPosition != "approve" || message.Variables["initiator"] != "alice" {
		t.Errorf("stored message = %+v", message)
	}

	// завершение: системная задача ведёт токен из задачи дальше, до End
	message.Variables["approved"] = true
	if err := s.moveNext(context.Background(), tx, &message, message.CurrentPosition, moveComplete); err != nil {
		t.Fatal(err)
	}
	if len(tx.inserted) != 1 {
		t.Errorf("complete must not create the task again: %v", tx.inserted)
	}

	// переменной исполнителя нет - задача создаётся без исполнителя, процесс не останавливается
	tx = &fakeUserTaskTx{}
	startAndActivate(t, s, q, tx, "main", nil)
	if len(tx.inserted) != 1 || tx.inserted[0][7] != "" || !slices.Equal(tx.inserted[0][9].([]string), []string{"managers"}) {
		t.Errorf("insert without initiator = %v, want task without assignee for managers", tx.inserted)
	}
}
