// Package metrics - метрики движка для Prometheus (client_golang). Collector регистрируется в реестре сервиса:
//
//	collector := metrics.NewCollector(pool, metrics.Options{})
//	prometheus.MustRegister(collector)
//	q := queue.New(pool, queue.Options{OnAttempt: collector.ObserveAttempt})
//
// Состояние очереди, экземпляров и задач пользователей считается запросами к базе при каждом опросе /metrics,
// поэтому оно одно на все поды: метрики с разных подов совпадают, в запросах Prometheus берите max без суммы.
// Время попыток задач - гистограмма своего пода из queue.Options.OnAttempt
package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/queue"
)

const (
	// задачи очереди, которые ещё не завершены: ждут выполнения или упали
	queueTasksQuery = `
select kind, status, count(*)
from bpm_queue
where status in ('pending', 'failed')
group by kind, status`

	// queueLagQuery - сколько ждёт самая старая задача, которую уже можно выдать. У выданной задачи run_at - конец
	// аренды в будущем, поэтому выполняемые задачи сюда не попадают
	queueLagQuery = `
select kind, extract(epoch from now() - min(run_at))::float8
from bpm_queue
where status = 'pending' and run_at <= now()
group by kind`

	// завершённых экземпляров много, и их число растёт без конца: считаются только выполняющиеся и упавшие
	instancesQuery = `
select process_name, status, count(*)
from bpm_process_instance
where status in ('running', 'failed')
group by process_name, status`

	openUserTasksQuery = `
select process_name, count(*)
from bpm_user_task
where status = 'created'
group by process_name`
)

// Options - параметры метрик, нулевые значения заменяются значениями по умолчанию
type Options struct {
	// Namespace - префикс имён метрик, по умолчанию "bpm"
	Namespace string
	// Timeout - на все запросы к базе одного опроса, по умолчанию 5s
	Timeout time.Duration
	// Buckets - границы гистограммы времени попыток в секундах, по умолчанию prometheus.DefBuckets
	Buckets []float64
	Logger  *slog.Logger
}

// Collector - prometheus.Collector метрик движка
type Collector struct {
	db      engine.Querier
	timeout time.Duration
	log     *slog.Logger

	queueTasks    *prometheus.Desc
	queueLag      *prometheus.Desc
	instances     *prometheus.Desc
	openUserTasks *prometheus.Desc
	scrapeSuccess *prometheus.Desc
	attempts      *prometheus.HistogramVec
}

// NewCollector - метрики по базе db (*pgxpool.Pool подходит)
func NewCollector(db engine.Querier, opts Options) *Collector {
	if opts.Namespace == "" {
		opts.Namespace = "bpm"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Buckets == nil {
		opts.Buckets = prometheus.DefBuckets
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	name := func(name string) string {
		return prometheus.BuildFQName(opts.Namespace, "", name)
	}
	return &Collector{
		db:      db,
		timeout: opts.Timeout,
		log:     opts.Logger,
		queueTasks: prometheus.NewDesc(name("queue_tasks"),
			"Незавершённые задачи очереди по виду и статусу: pending - ждут выполнения или выполняются, failed - упали",
			[]string{"kind", "status"}, nil),
		queueLag: prometheus.NewDesc(name("queue_lag_seconds"),
			"Сколько ждёт самая старая задача вида, которую уже можно выдать воркеру",
			[]string{"kind"}, nil),
		instances: prometheus.NewDesc(name("instances"),
			"Экземпляры процессов по статусу: running и failed",
			[]string{"process", "status"}, nil),
		openUserTasks: prometheus.NewDesc(name("user_tasks_open"),
			"Открытые задачи пользователей по процессу",
			[]string{"process"}, nil),
		scrapeSuccess: prometheus.NewDesc(name("scrape_success"),
			"1 - метрики из базы прочитаны, 0 - запрос к базе упал (ошибка в логе)",
			nil, nil),
		attempts: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: opts.Namespace,
			Name:      "queue_attempt_duration_seconds",
			Help:      "Попытки задач очереди этого пода: время обработчика и завершения, исход - done, retry, failed, cancelled, lost",
			Buckets:   opts.Buckets,
		}, []string{"kind", "result"}),
	}
}

// ObserveAttempt - queue.Options.OnAttempt: время и исход попытки задачи
func (c *Collector) ObserveAttempt(attempt queue.Attempt) {
	c.attempts.WithLabelValues(attempt.Kind, attempt.Result).Observe(attempt.Duration.Seconds())
}

// Describe - prometheus.Collector
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.queueTasks
	ch <- c.queueLag
	ch <- c.instances
	ch <- c.openUserTasks
	ch <- c.scrapeSuccess
	c.attempts.Describe(ch)
}

// Collect - prometheus.Collector: читает состояние из базы. Ошибка базы не ломает весь /metrics сервиса:
// метрики из базы пропускаются, а scrape_success становится 0
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.attempts.Collect(ch)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	var metrics []prometheus.Metric
	err := c.collectDB(ctx, &metrics)
	success := 1.0
	if err != nil {
		c.log.WarnContext(ctx, "bpm metrics: read from database", "error", err)
		success = 0
		metrics = nil
	}
	for _, metric := range metrics {
		ch <- metric
	}
	ch <- prometheus.MustNewConstMetric(c.scrapeSuccess, prometheus.GaugeValue, success)
}

// collectDB собирает метрики из базы в metrics; при ошибке собранное отбрасывается, чтобы не отдать половину
func (c *Collector) collectDB(ctx context.Context, metrics *[]prometheus.Metric) error {
	queries := []struct {
		query string
		desc  *prometheus.Desc
		// labels - сколько первых колонок - метки, последняя колонка - значение
		labels int
	}{
		{queueTasksQuery, c.queueTasks, 2},
		{queueLagQuery, c.queueLag, 1},
		{instancesQuery, c.instances, 2},
		{openUserTasksQuery, c.openUserTasks, 1},
	}
	for _, q := range queries {
		if err := c.gauges(ctx, q.query, q.desc, q.labels, metrics); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) gauges(ctx context.Context, query string, desc *prometheus.Desc, labels int, metrics *[]prometheus.Metric) error {
	rows, err := c.db.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]string, labels)
		dest := make([]any, 0, labels+1)
		for i := range values {
			dest = append(dest, &values[i])
		}
		var value float64
		dest = append(dest, &value)
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		*metrics = append(*metrics, prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, values...))
	}
	return rows.Err()
}
