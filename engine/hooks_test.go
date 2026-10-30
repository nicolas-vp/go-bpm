package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// TestHooks: start -> work (service task) -> check (метод) -> end. WrapCall видит обработчик и метод с попыткой задачи,
// OnElement - события элементов при выключенной истории, OnInstanceFailed - упавший шаг
func TestHooks(t *testing.T) {
	deployment := map[string]bpm.DeploymentProcess{
		"h": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"work"}},
			"work":  {ElementId: "work", ActivateType: bpm.ActivateTypeQueue, TaskType: "work", NextElements: []string{"check"}},
			"check": {ElementId: "check", ActivateType: bpm.ActivateTypeMethod, NextElements: []string{"end"},
				Method: func(_ context.Context, variables map[string]any) (map[string]any, error) { return variables, nil }},
			"end": {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	handler := func(_ context.Context, _ bpm.Job, variables map[string]any) (map[string]any, error) {
		return variables, nil
	}
	s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "work", Handler: handler}})

	var (
		calls    []Call
		elements []string
		failures []InstanceFailure
	)
	s.hooks = Hooks{
		OnElement: func(_ context.Context, record HistoryRecord) {
			elements = append(elements, record.Event+" "+record.ElementId)
		},
		WrapCall: func(ctx context.Context, call Call, next func(ctx context.Context) error) error {
			calls = append(calls, call)
			return next(ctx)
		},
		OnInstanceFailed: func(_ context.Context, failure InstanceFailure) {
			failures = append(failures, failure)
		},
	}

	startAndActivate(t, s, q, fakeExecTx{}, "h", nil, WithBusinessKey("order-1"))
	task := q.pop(t)
	task.Attempt, task.MaxAttempts = 2, 3
	if err := s.serviceTaskHandler("work", handler)(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	// шаг завершения service task, как его колбэк в очереди: метод check выполняется в этом шаге
	var message instanceMessage
	if err := json.Unmarshal(task.Payload, &message); err != nil {
		t.Fatal(err)
	}
	step := stepCallback(&task, func(ctx context.Context, tx queue.Tx) error {
		return s.moveNext(ctx, tx, &message, message.CurrentPosition, moveComplete)
	})
	if err := step(context.Background(), fakeExecTx{}); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want handler and method", calls)
	}
	if c := calls[0]; c.ElementId != "work" || c.TaskType != "work" || c.Attempt != 2 || c.LastAttempt || c.BusinessKey != "order-1" {
		t.Errorf("handler call = %+v", c)
	}
	if c := calls[1]; c.ElementId != "check" || c.TaskType != "" || c.Attempt != 2 {
		t.Errorf("method call = %+v, want attempt of the service task", c)
	}
	want := []string{"completed start", "activated work", "completed work", "completed check", "completed end"}
	if len(elements) != len(want) {
		t.Fatalf("elements = %v, want %v", elements, want)
	}
	for i := range want {
		if elements[i] != want[i] {
			t.Errorf("elements = %v, want %v", elements, want)
			break
		}
	}

	// упавший шаг: OnInstanceFailed с причиной из очереди
	payload, _ := json.Marshal(systemTask{Type: systemStart, Message: &instanceMessage{ProcessName: "h", CurrentPosition: "start"}})
	failed := &queue.Task{Kind: s.systemKind, Payload: payload, Id: 7, Attempt: 3, Err: errors.New("boom")}
	if err := s.systemTaskFailed(context.Background(), fakeExecTx{}, failed); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].TaskId != 7 || failures[0].ElementId != "start" || failures[0].Err == nil {
		t.Errorf("failures = %+v", failures)
	}
}
