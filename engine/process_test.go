package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/queue"
)

type fakeQueue struct {
	pushed []queue.Task
	// cancelled - ключи отменённых задач "<kind>/<key>"
	cancelled []string
}

func (q *fakeQueue) Register(string, queue.Handler, queue.KindOptions) {}

func (q *fakeQueue) Push(_ context.Context, task queue.Task, _ ...queue.PushOption) error {
	q.pushed = append(q.pushed, task)
	return nil
}

// InTx вызывает fn с транзакцией, которая принимает любые запросы: экземпляр, который пишет StartProcess
func (q *fakeQueue) InTx(_ context.Context, fn func(tx queue.Tx) error) error {
	return fn(fakeExecTx{})
}

type fakeExecTx struct{ queue.Tx }

func (fakeExecTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (q *fakeQueue) PushTx(ctx context.Context, _ queue.Tx, task queue.Task, opts ...queue.PushOption) error {
	return q.Push(ctx, task, opts...)
}

// CancelByKeyTx убирает из очереди задачи с ключом, как очередь отменяет ещё не завершённые
func (q *fakeQueue) CancelByKeyTx(_ context.Context, _ queue.Tx, kind, key string) error {
	q.cancelled = append(q.cancelled, fmt.Sprintf("%s/%s", kind, key))
	q.pushed = slices.DeleteFunc(q.pushed, func(task queue.Task) bool {
		return task.Kind == kind && task.Key == key
	})
	return nil
}

// popKind достаёт первую задачу вида kind: порядок задач разных видов в очереди не гарантирован
func (q *fakeQueue) popKind(t *testing.T, kind string) queue.Task {
	t.Helper()
	i := slices.IndexFunc(q.pushed, func(task queue.Task) bool { return task.Kind == kind })
	if i < 0 {
		t.Fatalf("no task of kind %s in queue", kind)
	}
	task := q.pushed[i]
	q.pushed = slices.Delete(q.pushed, i, i+1)
	return task
}

func (q *fakeQueue) pop(t *testing.T) queue.Task {
	t.Helper()
	if len(q.pushed) == 0 {
		t.Fatal("queue is empty")
	}
	task := q.pushed[0]
	q.pushed = q.pushed[1:]
	return task
}

// fakeSyncTx эмулирует таблицу bpm_parallel_sync.
type fakeSyncTx struct {
	queue.Tx
	arrived   map[string]int
	variables map[string]map[string]any
}

type fakeRow struct {
	arrived   int
	variables []byte
}

func (r fakeRow) Scan(dest ...any) error {
	*dest[0].(*int) = r.arrived
	*dest[1].(*[]byte) = r.variables
	return nil
}

func (tx *fakeSyncTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	hash := args[0].(string)
	var incoming map[string]any
	_ = json.Unmarshal([]byte(args[1].(string)), &incoming)
	if tx.variables[hash] == nil {
		tx.variables[hash] = map[string]any{}
	}
	for k, v := range incoming {
		tx.variables[hash][k] = v
	}
	tx.arrived[hash]++
	payload, _ := json.Marshal(tx.variables[hash])
	return fakeRow{arrived: tx.arrived[hash], variables: payload}
}

func (tx *fakeSyncTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	delete(tx.arrived, args[0].(string))
	return pgconn.CommandTag{}, nil
}

// generatedProcesses - процессы из сгенерированного кода с пустыми сервисами
var generatedProcesses = bpmgen.Processes(&services.Services{})

func newTestService(t *testing.T, deployment map[string]bpm.DeploymentProcess, queues []bpm.Queue) (*Engine, *fakeQueue) {
	t.Helper()
	return newTestServiceFromProcesses(t, map[string]bpm.Process{"test": {Flows: deployment}}, queues)
}

func newTestServiceFromProcesses(t *testing.T, processes map[string]bpm.Process, queues []bpm.Queue) (*Engine, *fakeQueue) {
	t.Helper()
	queue := &fakeQueue{}
	service, err := New(queue, Config{Logger: slog.New(slog.DiscardHandler)}, processes, queues)
	if err != nil {
		t.Fatal(err)
	}
	return service, queue
}

func startAndActivate(t *testing.T, s *Engine, q *fakeQueue, tx queue.Tx, processName string, variables map[string]any, opts ...StartOption) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.StartProcess(ctx, processName, variables, opts...); err != nil {
		t.Fatal(err)
	}
	var st systemTask
	if err := json.Unmarshal(q.pop(t).Payload, &st); err != nil {
		t.Fatal(err)
	}
	if err := s.startRoot(ctx, tx, st.Message); err != nil {
		t.Fatal(err)
	}
}

func completeTask(t *testing.T, s *Engine, q *fakeQueue, tx queue.Tx, wantType string) *instanceMessage {
	t.Helper()
	return completeTaskWith(t, s, q, tx, wantType, nil)
}

// completeTaskWith выполняет задачу wantType; handle, если задан, играет роль обработчика и может менять сообщение
func completeTaskWith(t *testing.T, s *Engine, q *fakeQueue, tx queue.Tx, wantType string, handle func(*instanceMessage)) *instanceMessage {
	t.Helper()
	task := q.pop(t)
	if task.Kind != s.taskKinds[wantType] {
		t.Fatalf("got task kind %s, want %q (%s)", task.Kind, wantType, s.taskKinds[wantType])
	}
	var message instanceMessage
	if err := json.Unmarshal(task.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if handle != nil {
		handle(&message)
	}
	if err := s.moveNext(context.Background(), tx, &message, message.CurrentPosition, moveComplete); err != nil {
		t.Fatal(err)
	}
	return &message
}

// TestGeneratedProcesses проверяет, что движок принимает все сгенерированные процессы
func TestGeneratedProcesses(t *testing.T) {
	s, _ := newTestServiceFromProcesses(t, generatedProcesses, bpmgen.Queues(&services.Services{}))
	for id, process := range generatedProcesses {
		if _, ok := s.deployment[id]; !ok {
			t.Errorf("process %q: deployment not found", id)
		}
		if !strings.Contains(process.BPMN, `id="`+id+`"`) {
			t.Errorf("process %q: BPMN must contain the process, got %.80q", id, process.BPMN)
		}
	}
}

// amountCondition - как сгенерированное условие на модели данных: amount > 100 -> sub, иначе default end.
// Переменные приходят из JSON очереди, поэтому amount там float64
var amountCondition = bpm.Condition(func(data *struct {
	Amount int `json:"amount"`
}) string {
	if data.Amount > 100 {
		return "sub"
	}
	return "end"
})

func TestConditionGatewayAndSubprocess(t *testing.T) {
	deployment := map[string]bpm.DeploymentProcess{
		"main": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start":    {ElementId: "start", CompleteType: bpm.CompleteTypeScript, Condition: amountCondition},
			"sub":      {ElementId: "sub", ActivateType: bpm.ActivateTypeStartSubprocess, ProcessId: "child", NextElements: []string{"afterSub"}},
			"afterSub": {ElementId: "afterSub", ActivateType: bpm.ActivateTypeQueue, TaskType: "after", NextElements: []string{"end"}},
			"end":      {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
		"child": {StartElement: "cStart", Expressions: map[string]*bpm.Expression{
			"cStart": {ElementId: "cStart", NextElements: []string{"cTask"}},
			"cTask":  {ElementId: "cTask", ActivateType: bpm.ActivateTypeQueue, TaskType: "child", NextElements: []string{"cEnd"}},
			"cEnd":   {ElementId: "cEnd", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "after"}, {TypeID: "child"}})

	startAndActivate(t, s, q, nil, "main", map[string]any{"amount": 50})
	if len(q.pushed) != 0 {
		t.Fatalf("amount <= 100 must go straight to end, got %d tasks", len(q.pushed))
	}

	startAndActivate(t, s, q, nil, "main", map[string]any{"amount": 500})
	// после End подпроцесса сообщение возвращается в родительский процесс
	returned := completeTask(t, s, q, nil, "child")
	if returned.ProcessName != "main" {
		t.Fatalf("got process %q, want main", returned.ProcessName)
	}
	after := completeTask(t, s, q, nil, "after")
	if after.ProcessName != "main" || len(after.ParentProcess) != 0 {
		t.Fatalf("must return to main without parents, got %q %v", after.ProcessName, after.ParentProcess)
	}
	if len(q.pushed) != 0 {
		t.Fatalf("process must be finished, got %d tasks", len(q.pushed))
	}
}

func TestParallelSync(t *testing.T) {
	deployment := map[string]bpm.DeploymentProcess{
		"par": {StartElement: "fork", Expressions: map[string]*bpm.Expression{
			"fork":  {ElementId: "fork", NextElements: []string{"a", "b"}},
			"a":     {ElementId: "a", ActivateType: bpm.ActivateTypeQueue, TaskType: "a", NextElements: []string{"join"}},
			"b":     {ElementId: "b", ActivateType: bpm.ActivateTypeQueue, TaskType: "b", NextElements: []string{"join"}},
			"join":  {ElementId: "join", ActivateType: bpm.ActivateTypeSyncQueue, ParallelSyncQuantity: 2, NextElements: []string{"after"}},
			"after": {ElementId: "after", ActivateType: bpm.ActivateTypeQueue, TaskType: "after", NextElements: []string{"end"}},
			"end":   {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "a"}, {TypeID: "b"}, {TypeID: "after"}})
	tx := &fakeSyncTx{arrived: map[string]int{}, variables: map[string]map[string]any{}}

	startAndActivate(t, s, q, tx, "par", nil)
	if len(q.pushed) != 2 {
		t.Fatalf("fork must create 2 tasks, got %d", len(q.pushed))
	}

	ctx := context.Background()
	for _, taskType := range []string{"a", "b"} {
		task := q.pop(t)
		if task.Kind != s.taskKinds[taskType] {
			t.Fatalf("got kind %s, want %q", task.Kind, taskType)
		}
		var message instanceMessage
		_ = json.Unmarshal(task.Payload, &message)
		message.Variables = map[string]any{taskType: true}
		if err := s.moveNext(ctx, tx, &message, message.CurrentPosition, moveComplete); err != nil {
			t.Fatal(err)
		}
		if taskType == "a" && len(q.pushed) != 1 {
			t.Fatalf("join must wait for the second branch, got %d tasks", len(q.pushed))
		}
	}

	after := completeTask(t, s, q, tx, "after")
	if after.Variables["a"] != true || after.Variables["b"] != true {
		t.Fatalf("join must merge branch variables, got %v", after.Variables)
	}
	if len(tx.arrived) != 0 {
		t.Fatalf("sync row must be deleted, got %v", tx.arrived)
	}
}

func TestMethods(t *testing.T) {
	var gotCtx context.Context
	// double - метод на переменных: читает a, пишет b. Переменные приходят из JSON очереди, поэтому a - float64
	double := func(ctx context.Context, variables map[string]any) (map[string]any, error) {
		gotCtx = ctx
		variables["b"] = variables["a"].(float64) * 2
		return variables, nil
	}
	deployment := map[string]bpm.DeploymentProcess{
		"m": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start":  {ElementId: "start", NextElements: []string{"double"}},
			"double": {ElementId: "double", ActivateType: bpm.ActivateTypeMethod, Method: double, NextElements: []string{"after"}},
			"after":  {ElementId: "after", ActivateType: bpm.ActivateTypeQueue, TaskType: "after", NextElements: []string{"end"}},
			"end":    {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "after"}})

	startAndActivate(t, s, q, nil, "m", map[string]any{"a": 21})
	if gotCtx == nil {
		t.Fatal("method must get ctx")
	}
	after := completeTask(t, s, q, nil, "after")
	if after.Variables["b"] != float64(42) || after.Variables["a"] != float64(21) {
		t.Fatalf("method variables must go further, got %v", after.Variables)
	}
}

// TestDataMethod - метод на модели данных (bpm.Method) получает сервисы, заданные при старте,
// а поля модели пишутся поверх переменных процесса
func TestDataMethod(t *testing.T) {
	type data struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	svc := &services.Services{}
	var gotServices *services.Services
	double := bpm.Method(svc, func(ctx context.Context, svc *services.Services, d *data) error {
		gotServices = svc
		d.B = d.A * 2
		return nil
	})
	deployment := map[string]bpm.DeploymentProcess{
		"m": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"double"}},
			"double": {ElementId: "double", ActivateType: bpm.ActivateTypeMethod, Method: double, NextElements: []string{"after"},
				Boundaries: []bpm.Boundary{{ElementId: "timeout", Type: bpm.BoundaryTypeTimer, Timer: time.Minute}}},
			"timeout": {ElementId: "timeout", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"end"}},
			"after":   {ElementId: "after", ActivateType: bpm.ActivateTypeQueue, TaskType: "after", NextElements: []string{"end"}},
			"end":     {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "after"}})

	startAndActivate(t, s, q, nil, "m", map[string]any{"a": 21, "other": "x"})
	if gotServices != svc {
		t.Fatalf("method must get services given at start, got %v", gotServices)
	}
	after := completeTask(t, s, q, nil, "after")
	if after.Variables["b"] != float64(42) || after.Variables["a"] != float64(21) || after.Variables["other"] != "x" {
		t.Fatalf("model fields must go over process variables, got %v", after.Variables)
	}
}

func TestMethodAndConditionErrors(t *testing.T) {
	failing := func(context.Context, map[string]any) (map[string]any, error) {
		return nil, errors.New("boom")
	}
	noMatch := func(map[string]any) (string, error) { return "", nil }
	badData := bpm.Condition(func(data *struct {
		Amount int `json:"amount"`
	}) string {
		return "end"
	})
	deployment := map[string]bpm.DeploymentProcess{
		"fail":    {StartElement: "m", Expressions: map[string]*bpm.Expression{"m": {ElementId: "m", ActivateType: bpm.ActivateTypeMethod, Method: failing}}},
		"noMatch": {StartElement: "g", Expressions: map[string]*bpm.Expression{"g": {ElementId: "g", CompleteType: bpm.CompleteTypeScript, Condition: noMatch}}},
		"noCond":  {StartElement: "g", Expressions: map[string]*bpm.Expression{"g": {ElementId: "g", CompleteType: bpm.CompleteTypeScript}}},
		// amount строкой не переводится в модель условия
		"badData": {StartElement: "g", Expressions: map[string]*bpm.Expression{"g": {ElementId: "g", CompleteType: bpm.CompleteTypeScript, Condition: badData}}},
		"noFunc":  {StartElement: "m", Expressions: map[string]*bpm.Expression{"m": {ElementId: "m", ActivateType: bpm.ActivateTypeMethod}}},
	}
	s, _ := newTestService(t, deployment, nil)
	for processName := range deployment {
		message := &instanceMessage{ProcessName: processName, Variables: map[string]any{"amount": "x"}}
		start := deployment[processName].StartElement
		if err := s.moveNext(context.Background(), nil, message, start, moveActivate); err == nil {
			t.Errorf("process %q: want error", processName)
		}
	}
}

func TestMergeProcesses(t *testing.T) {
	end := map[string]*bpm.Expression{"end": {ElementId: "end", ActivateType: bpm.ActivateTypeEnd}}
	processes := map[string]bpm.Process{
		"p1": {Flows: map[string]bpm.DeploymentProcess{"p1": {StartElement: "end", Expressions: end}, "p1.sub": {StartElement: "end", Expressions: end}}},
		"p2": {Flows: map[string]bpm.DeploymentProcess{"p2": {StartElement: "end", Expressions: end}}},
	}
	s, _ := newTestServiceFromProcesses(t, processes, nil)
	for _, flow := range []string{"p1", "p1.sub", "p2"} {
		if _, err := s.getProcess(flow); err != nil {
			t.Errorf("flow %q: %v", flow, err)
		}
	}

	processes["p3"] = bpm.Process{Flows: map[string]bpm.DeploymentProcess{"p1": {StartElement: "end", Expressions: end}}}
	if _, err := New(&fakeQueue{}, Config{}, processes, nil); err == nil {
		t.Fatal("duplicate flow must fail")
	}
}

func TestSharedQueue(t *testing.T) {
	// jobType shared - в двух процессах, очередь и обработчик у него одни
	end := map[string]*bpm.Expression{"end": {ElementId: "end", ActivateType: bpm.ActivateTypeEnd}}
	task := func() map[string]*bpm.Expression {
		return map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"task"}},
			"task":  {ElementId: "task", ActivateType: bpm.ActivateTypeQueue, TaskType: "shared", NextElements: []string{"end"}},
			"end":   end["end"],
		}
	}
	processes := map[string]bpm.Process{
		"p1": {Flows: map[string]bpm.DeploymentProcess{"p1": {StartElement: "start", Expressions: task()}}},
		"p2": {Flows: map[string]bpm.DeploymentProcess{"p2": {StartElement: "start", Expressions: task()}}},
	}
	s, q := newTestServiceFromProcesses(t, processes, []bpm.Queue{{TypeID: "shared", Retries: 3}})
	for _, process := range []string{"p1", "p2"} {
		startAndActivate(t, s, q, nil, process, nil)
		message := completeTask(t, s, q, nil, "shared")
		if message.ProcessName != process {
			t.Errorf("got task of process %q, want %q", message.ProcessName, process)
		}
	}

	if _, err := New(&fakeQueue{}, Config{}, processes,
		[]bpm.Queue{{TypeID: "shared"}, {TypeID: "shared"}}); err == nil {
		t.Fatal("duplicate task type in queues must fail")
	}
}

func TestHandlers(t *testing.T) {
	var gotJob bpm.Job
	// charge - обработчик jobType: читает amount, пишет charged
	charge := func(_ context.Context, job bpm.Job, variables map[string]any) (map[string]any, error) {
		gotJob = job
		variables["charged"] = variables["amount"]
		return variables, nil
	}
	deployment := map[string]bpm.DeploymentProcess{
		"h": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start":  {ElementId: "start", NextElements: []string{"charge"}},
			"charge": {ElementId: "charge", ActivateType: bpm.ActivateTypeQueue, TaskType: "payment.charge", NextElements: []string{"after"}},
			"after":  {ElementId: "after", ActivateType: bpm.ActivateTypeQueue, TaskType: "after", NextElements: []string{"end"}},
			"end":    {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "payment.charge"}, {TypeID: "after"}})
	ctx := context.Background()

	startAndActivate(t, s, q, nil, "h", map[string]any{"amount": 100})
	task := q.pop(t)
	if task.Kind != s.taskKinds["payment.charge"] {
		t.Fatalf("got task kind %s, want payment.charge", task.Kind)
	}
	var message instanceMessage
	if err := json.Unmarshal(task.Payload, &message); err != nil {
		t.Fatal(err)
	}

	if err := runHandler(ctx, charge, "payment.charge", &message, true); err != nil {
		t.Fatal(err)
	}
	wantJob := bpm.Job{InstanceUid: message.InstanceUid.String(), ProcessName: "h", ElementId: "charge", JobType: "payment.charge", LastAttempt: true}
	if gotJob.InstanceUid != wantJob.InstanceUid || gotJob.ProcessName != wantJob.ProcessName ||
		gotJob.ElementId != wantJob.ElementId || gotJob.JobType != wantJob.JobType || gotJob.LastAttempt != wantJob.LastAttempt {
		t.Fatalf("job = %+v, want %+v", gotJob, wantJob)
	}

	// то же, что делает OnSucceeded задачи очереди
	if err := s.moveNext(ctx, nil, &message, message.CurrentPosition, moveComplete); err != nil {
		t.Fatal(err)
	}
	after := completeTask(t, s, q, nil, "after")
	if after.Variables["charged"] != float64(100) || after.Variables["amount"] != float64(100) {
		t.Fatalf("handler variables must go further, got %v", after.Variables)
	}

	failing := func(context.Context, bpm.Job, map[string]any) (map[string]any, error) {
		return nil, errors.New("boom")
	}
	before := message.Variables
	if err := runHandler(ctx, failing, "payment.charge", &message, false); err == nil || err.Error() != "boom" {
		t.Fatalf("handler error must be returned as is for queue retry, got %v", err)
	}
	if message.Variables["charged"] != before["charged"] {
		t.Fatal("failed handler must not change variables")
	}
	if err := runHandler(ctx, nil, "payment.charge", &message, false); err == nil {
		t.Fatal("missing handler must fail the task")
	}
}

func TestGeneratedQueuesHaveHandlers(t *testing.T) {
	if _, err := New(&fakeQueue{}, Config{}, generatedProcesses, bpmgen.Queues(&services.Services{})); err != nil {
		t.Fatal(err)
	}
	for _, queue := range bpmgen.Queues(&services.Services{}) {
		if queue.Handler == nil {
			t.Errorf("task type %q: generated queue has no handler", queue.TypeID)
		}
	}
}
