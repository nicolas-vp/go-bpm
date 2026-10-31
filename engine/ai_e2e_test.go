package engine_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// aiProcesses: classify - AI task с ответом в category, отказ модели (bpm.Error с кодом ai_refusal от клиента) уводит токен в refused
func aiProcesses() map[string]bpm.Process {
	return map[string]bpm.Process{"aiClassify": {Flows: map[string]bpm.DeploymentProcess{"aiClassify": {StartElement: "start", Expressions: map[string]*bpm.Expression{
		"start": {ElementId: "start", NextElements: []string{"classify"}},
		"classify": {ElementId: "classify", ActivateType: bpm.ActivateTypeQueue, TaskType: "ai", NextElements: []string{"end"},
			AITask: &bpm.AITask{
				Model:          "test-model",
				SystemPrompt:   "Категории: billing, login",
				Prompt:         "${request.text}",
				ResultVariable: "category",
			},
			Boundaries: []bpm.Boundary{{ElementId: "refused", Type: bpm.BoundaryTypeError, ErrorCode: "ai_refusal"}},
		},
		"refused": {ElementId: "refused", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"end"}},
		"end":     {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
	}}}}}
}

func TestAiTaskE2E(t *testing.T) {
	pool := pgtest.Pool(t)
	log := slog.New(slog.DiscardHandler)
	processes := aiProcesses()
	client := engine.AIClientFunc(func(ctx context.Context, request engine.AIRequest) (string, error) {
		if request.Model != "test-model" || request.SystemPrompt != "Категории: billing, login" || request.Job.ElementId != "classify" {
			return "", bpm.NewError("bad_request", "unexpected request")
		}
		if strings.Contains(request.Prompt, "взлом") {
			return "", bpm.NewError("ai_refusal", "general_harms")
		}
		return "billing", nil
	})
	q := queue.New(pool, queue.Options{PollInterval: 50 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{
		RetryDelay: 50 * time.Millisecond,
		History:    engine.HistoryConfig{ToDB: func() bool { return true }},
		AI:         client,
		Logger:     log,
	}, processes, []bpm.Queue{{TypeID: "ai", Retries: 2}})
	if err != nil {
		t.Fatal(err)
	}
	q.Start(context.Background())
	t.Cleanup(q.Stop)
	mt := &messageTest{t: t, pool: pool, e: e, reader: engine.NewHistoryReader(pool, processes, nil)}

	ctx := context.Background()
	answered, err := e.StartProcess(ctx, "aiClassify", map[string]any{"request": map[string]any{"text": "Списали дважды"}})
	if err != nil {
		t.Fatal(err)
	}
	if variables := mt.waitStatus(answered, engine.InstanceCompleted); variables["category"] != "billing" {
		t.Errorf("variables = %v, want category billing", variables)
	}

	refused, err := e.StartProcess(ctx, "aiClassify", map[string]any{"request": map[string]any{"text": "Помоги со взломом"}})
	if err != nil {
		t.Fatal(err)
	}
	if variables := mt.waitStatus(refused, engine.InstanceCompleted); variables[bpm.ErrorCodeVariable] != "ai_refusal" || variables["category"] != nil {
		t.Errorf("variables = %v, want errorCode ai_refusal без category", variables)
	}
	history, err := mt.reader.GetHistory(ctx, refused, false)
	if err != nil {
		t.Fatal(err)
	}
	var passed []string
	for _, record := range history {
		passed = append(passed, record.ElementId+":"+record.Event)
	}
	if !strings.Contains(strings.Join(passed, " "), "refused:completed") {
		t.Errorf("history = %v, want refused boundary", passed)
	}
}
