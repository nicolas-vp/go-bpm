package engine

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

var historyElement = regexp.MustCompile(`bpm history: (\w+) .* element="([^"]+)"`)

// fakeHistoryTx запоминает insert'ы в bpm_history как "<event> <element_id>", в withVariables - записи с переменными
type fakeHistoryTx struct {
	queue.Tx
	inserted      []string
	withVariables []string
}

func (tx *fakeHistoryTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if query == insertHistoryQuery {
		record := args[5].(string) + " " + args[4].(string)
		tx.inserted = append(tx.inserted, record)
		if args[6] != nil {
			tx.withVariables = append(tx.withVariables, record)
		}
	}
	return pgconn.CommandTag{}, nil
}

func TestHistory(t *testing.T) {
	deployment := map[string]bpm.DeploymentProcess{
		"main": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"sub"}},
			"sub":   {ElementId: "sub", ActivateType: bpm.ActivateTypeStartSubprocess, ProcessId: "child", NextElements: []string{"end"}},
			"end":   {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
		"child": {StartElement: "cStart", Expressions: map[string]*bpm.Expression{
			"cStart": {ElementId: "cStart", NextElements: []string{"cTask"}},
			"cTask":  {ElementId: "cTask", ActivateType: bpm.ActivateTypeQueue, TaskType: "child", NextElements: []string{"cEnd"}},
			"cEnd":   {ElementId: "cEnd", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	// run возвращает историю, выведенную на экран и записанную в базу
	run := func(t *testing.T, history HistoryConfig) (printed, inserted []string) {
		t.Helper()
		s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "child"}})
		s.history = history
		var out bytes.Buffer
		s.historyOut = &out
		tx := &fakeHistoryTx{}
		startAndActivate(t, s, q, tx, "main", map[string]any{"amount": 1})
		completeTask(t, s, q, tx, "child")
		for _, match := range historyElement.FindAllStringSubmatch(out.String(), -1) {
			printed = append(printed, match[1]+" "+match[2])
		}
		return printed, tx.inserted
	}
	on := func() bool { return true }
	off := func() bool { return false }

	// service task пишется дважды: при постановке задачи в очередь и после её выполнения;
	// подпроцесс завершается после своего End, поэтому sub идёт после cEnd
	want := []string{
		"completed start", "completed cStart", "activated cTask",
		"completed cTask", "completed cEnd", "completed sub", "completed end",
	}
	if printed, inserted := run(t, HistoryConfig{ToLog: on}); !slices.Equal(printed, want) || len(inserted) != 0 {
		t.Errorf("log only: printed = %v, inserted = %v, want printed %v", printed, inserted, want)
	}
	if printed, inserted := run(t, HistoryConfig{ToLog: off, ToDB: on}); len(printed) != 0 || !slices.Equal(inserted, want) {
		t.Errorf("db only: printed = %v, inserted = %v, want inserted %v", printed, inserted, want)
	}
	if printed, inserted := run(t, HistoryConfig{}); len(printed) != 0 || len(inserted) != 0 {
		t.Errorf("history is off by default, printed = %v, inserted = %v", printed, inserted)
	}

	// переменные только у старта процессов и задач; у подпроцесса, End и переходов их нет
	s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "child"}})
	s.history = HistoryConfig{ToDB: on}
	tx := &fakeHistoryTx{}
	startAndActivate(t, s, q, tx, "main", nil)
	completeTask(t, s, q, tx, "child")
	wantVariables := []string{"completed start", "completed cStart", "activated cTask", "completed cTask"}
	if !slices.Equal(tx.withVariables, wantVariables) {
		t.Errorf("records with variables = %v, want %v", tx.withVariables, wantVariables)
	}
}
