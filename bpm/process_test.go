package bpm

import (
	"context"
	"testing"
	"time"
)

func TestBoundaries(t *testing.T) {
	expression := &Expression{Boundaries: []Boundary{
		{ElementId: "timer", Type: BoundaryTypeTimer, Timer: time.Second},
		{ElementId: "any", Type: BoundaryTypeError},
		{ElementId: "declined", Type: BoundaryTypeError, ErrorCode: "DECLINED"},
	}}
	if timers := expression.Timers(); len(timers) != 1 || timers[0].ElementId != "timer" {
		t.Errorf("timers = %v, want [timer]", timers)
	}
	for code, want := range map[string]string{"DECLINED": "declined", "OTHER": "any", "": "any"} {
		if got := expression.ErrorBoundary(code); got == nil || got.ElementId != want {
			t.Errorf("code %q: boundary = %v, want %s", code, got, want)
		}
	}
	// без события без кода ошибку с другим кодом никто не ловит
	expression.Boundaries = expression.Boundaries[2:]
	if got := expression.ErrorBoundary("OTHER"); got != nil {
		t.Errorf("code OTHER: boundary = %v, want nil", got)
	}
}

func TestError(t *testing.T) {
	if got := NewError("DECLINED", "limit").Error(); got != "bpmn error DECLINED: limit" {
		t.Errorf("Error() = %q", got)
	}
}

func TestRecordDecision(t *testing.T) {
	result := DecisionResult{DecisionId: "D", RuleIds: []string{"r1"}, Result: "x"}
	if err := RecordDecision(context.Background(), result); err != nil {
		t.Fatalf("без recorder: %v", err)
	}
	var got []DecisionResult
	ctx := WithDecisionRecorder(context.Background(), func(_ context.Context, r DecisionResult) error {
		got = append(got, r)
		return nil
	})
	if err := RecordDecision(ctx, result); err != nil || len(got) != 1 || got[0].RuleIds[0] != "r1" {
		t.Fatalf("got %+v, %v", got, err)
	}
}
