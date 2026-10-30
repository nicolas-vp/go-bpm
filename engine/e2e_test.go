package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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
		// задачи пользователей сами не завершатся, эти процессы проходят TestUserTasks и TestMultiInstance
		if name == "TestUserTask" || name == "TestMultiInstance" {
			continue
		}
		id, err := e.StartProcess(ctx, name, variables[name], engine.WithBusinessKey("key-"+name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		instances[name] = id
	}

	// таймеры демо-процессов - секунды
	waitQueue(t, pool, 30*time.Second)

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
		instance, err := reader.GetInstance(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if instance.ProcessName != name || instance.BusinessKey != "key-"+name || instance.Status != engine.InstanceCompleted ||
			instance.ParentInstanceUid != nil || instance.UpdatedAt.Before(instance.CreatedAt) {
			t.Errorf("%s: instance = %+v", name, instance)
		}
		found, err := reader.ListInstances(ctx, engine.InstanceFilter{ProcessName: name, Status: engine.InstanceCompleted, BusinessKey: "key-" + name})
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 1 || found[0].InstanceUid != id {
			t.Errorf("%s: list instances = %+v", name, found)
		}
		// период запуска: From включительно, To - нет
		period := engine.InstanceFilter{ProcessName: name, BusinessKey: "key-" + name, From: instance.CreatedAt, To: instance.CreatedAt.Add(time.Microsecond)}
		if found, err := reader.ListInstances(ctx, period); err != nil || len(found) != 1 || found[0].InstanceUid != id {
			t.Errorf("%s: instances started at %s = %+v, err %v", name, instance.CreatedAt, found, err)
		}
		if found, err := reader.ListInstances(ctx, engine.InstanceFilter{ProcessName: name, BusinessKey: "key-" + name, To: instance.CreatedAt}); err != nil || len(found) != 0 {
			t.Errorf("%s: instances before start = %+v, err %v", name, found, err)
		}

		// термокарта: элементы процесса и его встроенных подпроцессов, каждый завершился хотя бы раз;
		// экземпляры - все экземпляры процесса, в том числе вызванные callActivity другого процесса
		heatmap, err := reader.GetHeatmap(ctx, engine.HeatmapFilter{ProcessName: name})
		if err != nil {
			t.Fatal(err)
		}
		ofProcess, err := reader.ListInstances(ctx, engine.InstanceFilter{ProcessName: name})
		if err != nil {
			t.Fatal(err)
		}
		if heatmap.Instances != len(ofProcess) || heatmap.Truncated || len(heatmap.Elements) == 0 {
			t.Errorf("%s: heatmap = %+v", name, heatmap)
		}
		for _, element := range heatmap.Elements {
			known := slices.ContainsFunc(slices.Collect(maps.Values(processes[name].Flows)), func(flow bpm.DeploymentProcess) bool {
				return flow.Expressions[element.ElementId] != nil
			})
			if !known || element.Count == 0 || element.Active != 0 || element.MaxMs < element.AvgMs {
				t.Errorf("%s: heatmap element = %+v", name, element)
			}
		}
		if past, err := reader.GetHeatmap(ctx, engine.HeatmapFilter{ProcessName: name, To: instance.CreatedAt}); err != nil || past.Instances != 0 {
			t.Errorf("%s: heatmap before start = %+v, err %v", name, past, err)
		}

		// динамика: все экземпляры процесса завершены, сумма по дням - итог
		moscow, err := time.LoadLocation("Europe/Moscow")
		if err != nil {
			t.Fatal(err)
		}
		stats, err := reader.GetProcessStats(ctx, engine.StatsFilter{ProcessName: name, Location: moscow,
			From: instance.CreatedAt.Add(-48 * time.Hour), To: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		started := 0
		for _, day := range stats.Days {
			started += day.Started
		}
		total := stats.Total
		if total.Started != len(ofProcess) || total.Completed != total.Started || started != total.Started ||
			total.MedianMs == nil || total.P95Ms == nil || *total.P95Ms < *total.MedianMs || len(stats.Days) < 3 ||
			stats.Days[len(stats.Days)-1].Day != time.Now().Add(time.Hour).In(moscow).Format(time.DateOnly) {
			t.Errorf("%s: stats = %+v", name, stats)
		}
	}
	if _, err := reader.GetHeatmap(ctx, engine.HeatmapFilter{ProcessName: "unknown"}); !errors.Is(err, engine.ErrProcessNotFound) {
		t.Errorf("heatmap of unknown process: err = %v", err)
	}

	// дочерние экземпляры (подпроцессы, callActivity) тоже завершены и наследуют бизнес-ключ
	all, err := reader.ListInstances(ctx, engine.InstanceFilter{Limit: engine.MaxInstancesLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range all {
		if instance.Status != engine.InstanceCompleted || instance.BusinessKey == "" {
			t.Errorf("instance = %+v", instance)
		}
	}
	if running, err := reader.ListInstances(ctx, engine.InstanceFilter{Status: engine.InstanceRunning}); err != nil || len(running) != 0 {
		t.Errorf("running instances = %+v, err %v", running, err)
	}
	// страница: before отсекает уже полученные
	if len(all) > 1 {
		page, err := reader.ListInstances(ctx, engine.InstanceFilter{Before: all[0].InstanceUid, Limit: 1})
		if err != nil || len(page) != 1 || page[0].InstanceUid != all[1].InstanceUid {
			t.Errorf("page after %s = %+v, err %v", all[0].InstanceUid, page, err)
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

// waitQueue ждёт, пока в очереди не останется задач pending; упавшая задача - ошибка теста
func waitQueue(t *testing.T, pool *pgxpool.Pool, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
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
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d tasks still pending", pending)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
