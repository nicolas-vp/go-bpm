package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// fakeWaitTx эмулирует таблицу bpm_wait
type fakeWaitTx struct {
	queue.Tx
	t     *testing.T
	waits map[uuid.UUID]string
}

func newFakeWaitTx(t *testing.T) *fakeWaitTx {
	return &fakeWaitTx{t: t, waits: map[uuid.UUID]string{}}
}

func (tx *fakeWaitTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if query != insertWaitQuery {
		tx.t.Fatalf("unexpected exec %q", query)
	}
	tx.waits[args[0].(uuid.UUID)] = args[2].(string)
	return pgconn.CommandTag{}, nil
}

func (tx *fakeWaitTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	if query != deleteWaitQuery {
		tx.t.Fatalf("unexpected query %q", query)
	}
	waitId := args[0].(uuid.UUID)
	_, found := tx.waits[waitId]
	delete(tx.waits, waitId)
	return countRow{found: found}
}

type countRow struct{ found bool }

func (r countRow) Scan(dest ...any) error {
	*dest[0].(*int) = 0
	if r.found {
		*dest[0].(*int) = 1
	}
	return nil
}

// timerDeployment: start -> wait (таймер 3s) -> work (service task) -> end.
// У work граничные события: таймер timeout 1s -> afterTimeout, ошибка DECLINED -> declined, любая ошибка -> anyError
func timerDeployment(method bpm.MethodFunc) map[string]bpm.DeploymentProcess {
	boundaries := []bpm.Boundary{
		{ElementId: "anyError", Type: bpm.BoundaryTypeError},
		{ElementId: "declined", Type: bpm.BoundaryTypeError, ErrorCode: "DECLINED"},
		{ElementId: "timeout", Type: bpm.BoundaryTypeTimer, Timer: time.Second},
	}
	return map[string]bpm.DeploymentProcess{
		"t": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start":        {ElementId: "start", NextElements: []string{"wait"}},
			"wait":         {ElementId: "wait", ActivateType: bpm.ActivateTypeTimer, Timer: 3 * time.Second, NextElements: []string{"work"}},
			"work":         {ElementId: "work", ActivateType: bpm.ActivateTypeQueue, TaskType: "work", NextElements: []string{"end"}, Boundaries: boundaries},
			"end":          {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
			"timeout":      {ElementId: "timeout", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"afterTimeout"}},
			"afterTimeout": {ElementId: "afterTimeout", ActivateType: bpm.ActivateTypeQueue, TaskType: "afterTimeout", NextElements: []string{"end"}},
			"declined":     {ElementId: "declined", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"endDeclined"}},
			"endDeclined":  {ElementId: "endDeclined", ActivateType: bpm.ActivateTypeEnd},
			"anyError":     {ElementId: "anyError", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"endAny"}},
			"endAny":       {ElementId: "endAny", ActivateType: bpm.ActivateTypeEnd},
		}},
		// m - то же для task: метод с теми же граничными событиями
		"m": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start":        {ElementId: "start", NextElements: []string{"work"}},
			"work":         {ElementId: "work", ActivateType: bpm.ActivateTypeMethod, Method: method, NextElements: []string{"end"}, Boundaries: boundaries},
			"end":          {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
			"timeout":      {ElementId: "timeout", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"afterTimeout"}},
			"afterTimeout": {ElementId: "afterTimeout", ActivateType: bpm.ActivateTypeQueue, TaskType: "afterTimeout", NextElements: []string{"end"}},
			"declined":     {ElementId: "declined", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"endDeclined"}},
			"endDeclined":  {ElementId: "endDeclined", ActivateType: bpm.ActivateTypeEnd},
			"anyError":     {ElementId: "anyError", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"endAny"}},
			"endAny":       {ElementId: "endAny", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
}

type timerTest struct {
	t   *testing.T
	s   *Engine
	q   *fakeQueue
	tx  *fakeWaitTx
	out *bytes.Buffer
}

func newTimerTest(t *testing.T, method bpm.MethodFunc) *timerTest {
	s, q := newTestService(t, timerDeployment(method), []bpm.Queue{{TypeID: "work"}, {TypeID: "afterTimeout"}})
	out := &bytes.Buffer{}
	s.history = HistoryConfig{ToLog: func() bool { return true }}
	s.historyOut = out
	return &timerTest{t: t, s: s, q: q, tx: newFakeWaitTx(t), out: out}
}

// popTimer достаёт системную задачу таймера; таймеры не ждут: тест сам решает, когда они срабатывают
func (tt *timerTest) popTimer() systemTask {
	tt.t.Helper()
	task := tt.q.popKind(tt.t, tt.s.systemKind)
	var st systemTask
	if err := json.Unmarshal(task.Payload, &st); err != nil {
		tt.t.Fatal(err)
	}
	if st.Type != systemTimer {
		tt.t.Fatalf("got system task %q, want timer", st.Type)
	}
	return st
}

func (tt *timerTest) fire(st systemTask) {
	tt.t.Helper()
	if err := tt.s.fireTimer(context.Background(), tt.tx, st.Message, st.Boundary); err != nil {
		tt.t.Fatal(err)
	}
}

// popJob достаёт задачу service task: воркер взял её в работу
func (tt *timerTest) popJob(taskType string) *instanceMessage {
	tt.t.Helper()
	task := tt.q.popKind(tt.t, tt.s.taskKinds[taskType])
	var message instanceMessage
	if err := json.Unmarshal(task.Payload, &message); err != nil {
		tt.t.Fatal(err)
	}
	return &message
}

func (tt *timerTest) complete(message *instanceMessage) {
	tt.t.Helper()
	if err := tt.s.moveNext(context.Background(), tt.tx, message, message.CurrentPosition, moveComplete); err != nil {
		tt.t.Fatal(err)
	}
}

// startWork запускает процесс t и проводит его через промежуточный таймер до задачи work
func (tt *timerTest) startWork() {
	tt.t.Helper()
	startAndActivate(tt.t, tt.s, tt.q, tt.tx, "t", map[string]any{"a": 1})
	wait := tt.popTimer()
	if wait.Boundary != "" || wait.Delay != 3*time.Second || wait.Message.CurrentPosition != "wait" {
		tt.t.Fatalf("intermediate timer = %+v, want wait for 3s", wait)
	}
	if len(tt.q.pushed) != 0 {
		tt.t.Fatalf("process must wait for the timer, got %d tasks", len(tt.q.pushed))
	}
	tt.fire(wait)
	// задача и её граничный таймер
	if len(tt.q.pushed) != 2 || len(tt.tx.waits) != 1 {
		tt.t.Fatalf("work must push a job and a timer and wait, got %d tasks, %d waits", len(tt.q.pushed), len(tt.tx.waits))
	}
}

func (tt *timerTest) history() []string {
	var history []string
	for _, match := range historyElement.FindAllStringSubmatch(tt.out.String(), -1) {
		history = append(history, match[1]+" "+match[2])
	}
	return history
}

func (tt *timerTest) wantHistoryTail(want ...string) {
	tt.t.Helper()
	history := tt.history()
	if len(history) < len(want) || !slices.Equal(history[len(history)-len(want):], want) {
		tt.t.Errorf("history = %v, want tail %v", history, want)
	}
}

func TestTimerTaskCompletesFirst(t *testing.T) {
	tt := newTimerTest(t, nil)
	tt.startWork()
	job := tt.popJob("work")
	timer := tt.popTimer()
	if timer.Boundary != "timeout" || timer.Delay != time.Second || timer.Message.WaitId != job.WaitId {
		t.Fatalf("boundary timer = %+v, want timeout for 1s with the job wait", timer)
	}

	tt.complete(job)
	if len(tt.tx.waits) != 0 || len(tt.q.pushed) != 0 {
		t.Fatalf("process must finish, got %d waits, %d tasks", len(tt.tx.waits), len(tt.q.pushed))
	}
	// таймер уже взят в работу: отмена его не остановит, он проиграет на bpm_wait
	tt.fire(timer)
	if len(tt.q.pushed) != 0 {
		t.Fatalf("late timer must do nothing, got %d tasks", len(tt.q.pushed))
	}
	tt.wantHistoryTail("activated wait", "completed wait", "activated work", "completed work", "completed end")
}

func TestTimerCancelsQueuedTimer(t *testing.T) {
	tt := newTimerTest(t, nil)
	tt.startWork()
	job := tt.popJob("work")
	waitId := job.WaitId
	// таймер ещё в очереди: завершение задачи его отменяет
	tt.complete(job)
	if len(tt.q.pushed) != 0 || !slices.Contains(tt.q.cancelled, "bpm.system/"+timerKey(waitId, "timeout")) {
		t.Fatalf("timer must be cancelled, got tasks %d, cancelled %v", len(tt.q.pushed), tt.q.cancelled)
	}
	if job.WaitId != uuid.Nil {
		t.Errorf("wait must be cleared after the task, got %s", job.WaitId)
	}
}

func TestTimerFiresFirst(t *testing.T) {
	for _, jobStarted := range []bool{false, true} {
		tt := newTimerTest(t, nil)
		tt.startWork()
		var job *instanceMessage
		if jobStarted {
			job = tt.popJob("work")
		}
		tt.fire(tt.popTimer())
		// задача, которую воркер ещё не взял, отменяется, и в очереди только задача ветки таймера
		after := tt.popJob("afterTimeout")
		if len(tt.q.pushed) != 0 || len(tt.tx.waits) != 0 {
			t.Fatalf("job started %v: got %d tasks, %d waits, want only afterTimeout", jobStarted, len(tt.q.pushed), len(tt.tx.waits))
		}
		if after.Variables["a"] != float64(1) || after.WaitId != uuid.Nil {
			t.Errorf("timer branch must keep variables and clear wait, got %v %s", after.Variables, after.WaitId)
		}
		tt.wantHistoryTail("activated work", "terminated work", "completed timeout", "activated afterTimeout")

		if jobStarted {
			// задача досчиталась после таймера: её результат не нужен
			tt.complete(job)
			if len(tt.q.pushed) != 0 {
				t.Fatalf("late job must do nothing, got %d tasks", len(tt.q.pushed))
			}
			tt.wantHistoryTail("terminated work", "completed timeout", "activated afterTimeout")
		}
	}
}

func TestServiceTaskErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		lastAttempt bool
		wantEnd     string
		wantCode    string
		wantErr     error
	}{
		{name: "business error with code", err: bpm.NewError("DECLINED", "limit"), wantEnd: "endDeclined", wantCode: "DECLINED"},
		{name: "business error without its boundary", err: bpm.NewError("OTHER", ""), wantEnd: "endAny", wantCode: "OTHER"},
		{name: "technical error is retried", err: errors.New("timeout")},
		{name: "technical error after retries", err: errors.New("timeout"), lastAttempt: true, wantEnd: "endAny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tt := newTimerTest(t, nil)
			tt.startWork()
			job := tt.popJob("work")
			step, err := tt.s.taskErrorStep(context.Background(), tc.lastAttempt, "work", job, tc.err)
			if tc.wantEnd == "" {
				if step != nil || !errors.Is(err, tc.err) {
					t.Fatalf("got step %v, err %v; want the error for retry", step != nil, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := step(context.Background(), tt.tx); err != nil {
				t.Fatal(err)
			}
			// по событию ошибки задача прерывается, её таймер отменяется
			if len(tt.q.pushed) != 0 || len(tt.tx.waits) != 0 {
				t.Fatalf("got %d tasks, %d waits, want finished process", len(tt.q.pushed), len(tt.tx.waits))
			}
			if job.Variables[bpm.ErrorCodeVariable] != tc.wantCode || job.Variables[bpm.ErrorMessageVariable] == nil {
				t.Errorf("variables = %v, want errorCode %q and errorMessage", job.Variables, tc.wantCode)
			}
			tt.wantHistoryTail("terminated work", "completed "+map[string]string{"endDeclined": "declined", "endAny": "anyError"}[tc.wantEnd], "completed "+tc.wantEnd)
		})
	}

	// без граничного события бизнес-ошибка не повторяется
	s, q := newTestService(t, map[string]bpm.DeploymentProcess{"p": {StartElement: "start", Expressions: map[string]*bpm.Expression{
		"start": {ElementId: "start", NextElements: []string{"work"}},
		"work":  {ElementId: "work", ActivateType: bpm.ActivateTypeQueue, TaskType: "work", NextElements: []string{"end"}},
		"end":   {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
	}}}, []bpm.Queue{{TypeID: "work"}})
	startAndActivate(t, s, q, nil, "p", nil)
	var message instanceMessage
	_ = json.Unmarshal(q.pop(t).Payload, &message)
	if _, err := s.taskErrorStep(context.Background(), false, "work", &message, bpm.NewError("DECLINED", "")); !errors.Is(err, queue.ErrNonRetryable) {
		t.Errorf("uncaught business error must not be retried, got %v", err)
	}
}

func TestMethodBoundaries(t *testing.T) {
	var result error
	// work - метод, который завершается по result или ждёт отмены контекста
	work := func(ctx context.Context, variables map[string]any) (map[string]any, error) {
		if result != nil {
			return nil, result
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	for _, tc := range []struct {
		name    string
		result  error
		want    []string
		wantErr bool
	}{
		// метод слушает ctx: через секунду граничного таймера он прерывается
		{name: "timer", want: []string{"terminated work", "completed timeout", "activated afterTimeout"}},
		{name: "business error", result: bpm.NewError("DECLINED", ""), want: []string{"terminated work", "completed declined", "completed endDeclined"}},
		// техническая ошибка метода откатывает шаг, очередь его повторит
		{name: "technical error", result: errors.New("boom"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result = tc.result
			tt := newTimerTest(t, work)
			if _, err := tt.s.StartProcess(context.Background(), "m", nil); err != nil {
				t.Fatal(err)
			}
			var st systemTask
			_ = json.Unmarshal(tt.q.pop(t).Payload, &st)
			started := time.Now()
			err := tt.s.moveNext(context.Background(), tt.tx, st.Message, st.Message.CurrentPosition, moveActivate)
			if tc.wantErr {
				if err == nil || len(tt.q.pushed) != 0 {
					t.Fatalf("got err %v and %d tasks, want error", err, len(tt.q.pushed))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.result == nil && time.Since(started) < time.Second {
				t.Errorf("method timer fired after %v, want 1s", time.Since(started))
			}
			tt.wantHistoryTail(tc.want...)
		})
	}
}
