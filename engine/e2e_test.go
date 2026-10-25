package engine_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// TestEndToEnd гоняет все демо-процессы на настоящей очереди и базе: каждый экземпляр должен дойти до End event
// корневого процесса, без упавших задач, а история - читаться обратно со своей диаграммой
func TestEndToEnd(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	on := func() bool { return true }

	svc := &services.Services{}
	processes := bpmgen.Processes(svc)
	q := queue.New(pool, queue.Options{PollInterval: 200 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{
		RetryDelay:   100 * time.Millisecond,
		History:      engine.HistoryConfig{ToDB: on},
		RulesHistory: engine.HistoryConfig{ToDB: on},
		Logger:       log,
	}, processes, bpmgen.Queues(svc))
	if err != nil {
		t.Fatal(err)
	}
	q.Start(ctx)
	defer q.Stop()

	variables := map[string]map[string]any{
		"TestBpmProcess": {"amount": 55},
		"TestDecision":   {"a": "some", "b": "x"},
	}
	instances := make(map[string]uuid.UUID)
	for name := range processes {
		id, err := e.StartProcess(ctx, name, variables[name])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		instances[name] = id
	}

	// таймеры демо-процессов - секунды
	deadline := time.Now().Add(30 * time.Second)
	for {
		var pending, failed int
		err := pool.QueryRow(ctx, `select count(*) filter (where status = 'pending'), count(*) filter (where status = 'failed') from bpm_queue`).Scan(&pending, &failed)
		if err != nil {
			t.Fatal(err)
		}
		if failed > 0 {
			var lastError string
			_ = pool.QueryRow(ctx, `select kind || ': ' || coalesce(last_error, '') from bpm_queue where status = 'failed' limit 1`).Scan(&lastError)
			t.Fatalf("%d failed tasks, first: %s", failed, lastError)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d tasks still pending", pending)
		}
		time.Sleep(100 * time.Millisecond)
	}

	reader := engine.NewHistoryReader(pool, processes, bpmgen.Decisions)
	for name, id := range instances {
		records, err := reader.GetHistory(ctx, id, false)
		if err != nil {
			t.Fatal(err)
		}
		flow := processes[name].Flows[name]
		ended := slices.ContainsFunc(records, func(r engine.HistoryRecord) bool {
			expression, found := flow.Expressions[r.ElementId]
			return found && r.Event == engine.HistoryCompleted && expression.ActivateType == bpm.ActivateTypeEnd
		})
		if !ended {
			t.Errorf("%s: instance %s did not reach an end event, history: %v", name, id, records)
		}
		if _, err := reader.GetDiagram(ctx, id); err != nil {
			t.Errorf("%s: diagram: %v", name, err)
		}
	}

	rules, err := reader.GetRulesHistory(ctx, instances["TestDecision"], "Activity_0xzljrt")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules.Records) != 1 || rules.DMN["Decision_1"] == "" {
		t.Errorf("rules history = %+v", rules.Records)
	}
}
