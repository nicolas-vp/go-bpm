package metrics_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/metrics"
	"github.com/nicolas-vp/go-bpm/queue"
)

// gather собирает метрики коллектора строками "<имя>{<метки>} <значение>"; у гистограммы значение - число наблюдений
func gather(t *testing.T, collector prometheus.Collector) map[string]float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]float64)
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			var labels []string
			for _, label := range metric.GetLabel() {
				labels = append(labels, label.GetName()+"="+label.GetValue())
			}
			key := family.GetName() + "{" + strings.Join(labels, ",") + "}"
			switch family.GetType() {
			case dto.MetricType_HISTOGRAM:
				result[key] = float64(metric.GetHistogram().GetSampleCount())
			default:
				result[key] = metric.GetGauge().GetValue()
			}
		}
	}
	return result
}

func exec(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestCollector(t *testing.T) {
	pool := pgtest.Pool(t)
	exec(t, pool, `
insert into bpm_queue (kind, payload, status, run_at) values
    ('bpm.system', '{}', 'pending', now() - interval '30 seconds'),
    ('bpm.system', '{}', 'pending', now() - interval '5 seconds'),
    ('bpm.system', '{}', 'done', now() - interval '1 hour'),
    ('bpm.task.charge', '{}', 'failed', now()),
    ('bpm.task.charge', '{}', 'pending', now() + interval '1 hour')`)
	for _, status := range []string{"running", "running", "failed", "completed"} {
		exec(t, pool, `insert into bpm_process_instance (instance_uid, process_name, status) values ($1, 'order', $2)`,
			uuid.Must(uuid.NewV7()), status)
	}
	for _, status := range []string{"created", "created", "completed"} {
		exec(t, pool, `insert into bpm_user_task (task_uid, instance_uid, process_name, element_id, status, message)
values ($1, $2, 'order', 'approve', $3, '{}')`, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), status)
	}

	collector := metrics.NewCollector(pool, metrics.Options{Logger: slog.New(slog.DiscardHandler)})
	collector.ObserveAttempt(queue.Attempt{Kind: "bpm.system", Result: queue.AttemptDone, Duration: 20 * time.Millisecond})
	collector.ObserveAttempt(queue.Attempt{Kind: "bpm.system", Result: queue.AttemptDone, Duration: time.Second})
	got := gather(t, collector)

	for key, want := range map[string]float64{
		"bpm_queue_tasks{kind=bpm.system,status=pending}":                 2,
		"bpm_queue_tasks{kind=bpm.task.charge,status=failed}":             1,
		"bpm_queue_tasks{kind=bpm.task.charge,status=pending}":            1,
		"bpm_instances{process=order,status=running}":                     2,
		"bpm_instances{process=order,status=failed}":                      1,
		"bpm_user_tasks_open{process=order}":                              2,
		"bpm_queue_attempt_duration_seconds{kind=bpm.system,result=done}": 2,
		"bpm_scrape_success{}":                                            1,
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	// задача, отложенная в будущее (или выданная воркеру), не ждёт: лага по виду нет
	if lag := got["bpm_queue_lag_seconds{kind=bpm.system}"]; lag < 29 || lag > 60 {
		t.Errorf("lag bpm.system = %v, want ~30s", lag)
	}
	if _, found := got["bpm_queue_lag_seconds{kind=bpm.task.charge}"]; found {
		t.Error("lag bpm.task.charge: задача отложена, лага быть не должно")
	}
	if _, found := got["bpm_instances{process=order,status=completed}"]; found {
		t.Error("завершённые экземпляры не считаются")
	}
}

// TestCollectorDatabaseDown - упавшая база не ломает /metrics: метрик из базы нет, scrape_success = 0
func TestCollectorDatabaseDown(t *testing.T) {
	pool := pgtest.Pool(t)
	pool.Close()
	collector := metrics.NewCollector(pool, metrics.Options{Logger: slog.New(slog.DiscardHandler)})
	got := gather(t, collector)
	if got["bpm_scrape_success{}"] != 0 || len(got) != 1 {
		t.Errorf("metrics = %v, want only bpm_scrape_success 0", got)
	}
}
