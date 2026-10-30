// Демо-сервис go-bpm: процессы из examples/demo/process, JSON API и консоль /ui/ с историей экземпляров на одном порту.
//
//	go run ./examples/demo/cmd/demo -dsn postgres://postgres:postgres@localhost:5432/go_bpm?sslmode=disable
//
// Запуск процесса, поиск экземпляров и история:
//
//	curl -XPOST localhost:8080/api/v1/processes/TestDecision/instances -d '{"variables":{"a":"some","b":"x"},"businessKey":"order-1"}'
//	curl 'localhost:8080/api/v1/instances?processName=TestDecision&status=completed'
//
// Задачи пользователей: очередь группы, взять в работу, завершить:
//
//	curl -XPOST localhost:8080/api/v1/processes/TestUserTask/instances -d '{"variables":{"initiator":"alice"}}'
//	curl 'localhost:8080/api/v1/tasks?candidateUser=bob&candidateGroup=managers'
//	curl -XPOST localhost:8080/api/v1/tasks/<task id>/claim -d '{"userId":"bob","groups":["managers"]}'
//	curl -XPOST localhost:8080/api/v1/tasks/<task id>/complete -d '{"userId":"bob","variables":{"approved":true}}'
//	open http://localhost:8080/ui/history/?instanceId=<id>
//
// Метрики Prometheus:
//
//	curl localhost:8080/metrics
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/httpapi"
	"github.com/nicolas-vp/go-bpm/metrics"
	"github.com/nicolas-vp/go-bpm/migrations"
	"github.com/nicolas-vp/go-bpm/queue"
	"github.com/nicolas-vp/go-bpm/webui"
)

func main() {
	var (
		dsn            = flag.String("dsn", env("GO_BPM_DSN", "postgres://postgres:postgres@localhost:5432/go_bpm?sslmode=disable"), "PostgreSQL DSN, или GO_BPM_DSN")
		addr           = flag.String("addr", env("GO_BPM_ADDR", ":8080"), "адрес HTTP-сервера, или GO_BPM_ADDR")
		kindPrefix     = flag.String("kind-prefix", engine.DefaultKindPrefix, "префикс видов задач: разводит по одной базе экземпляры с разным кодом")
		historyLog     = flag.Bool("history-log", true, "писать историю элементов на экран")
		historyDB      = flag.Bool("history-db", true, "писать историю элементов в bpm_history")
		rulesLog       = flag.Bool("rules-log", true, "писать историю бизнес-правил на экран")
		rulesDB        = flag.Bool("rules-db", true, "писать историю бизнес-правил в bpm_rules_history")
		skipMigrations = flag.Bool("skip-migrations", false, "не применять миграции при старте")
		retention      = flag.Duration("retention", envDuration("GO_BPM_RETENTION", 0), "сколько хранить историю завершённых и отменённых экземпляров, 0 - не удалять; или GO_BPM_RETENTION")
	)
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log, *dsn, *addr, *kindPrefix, *skipMigrations, *retention, engine.HistoryConfig{
		ToLog: flagFunc(*historyLog), ToDB: flagFunc(*historyDB),
	}, engine.HistoryConfig{
		ToLog: flagFunc(*rulesLog), ToDB: flagFunc(*rulesDB),
	}); err != nil {
		log.Error("demo", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, dsn, addr, kindPrefix string, skipMigrations bool, retention time.Duration,
	history, rulesHistory engine.HistoryConfig) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	if !skipMigrations {
		if err := migrations.Up(ctx, pool); err != nil {
			return err
		}
	}

	// внешние сервисы для методов task и обработчиков jobType
	svc := &services.Services{}
	processes := bpmgen.Processes(svc)
	// метрики: состояние движка из базы, время попыток задач, рантайм Go - на /metrics
	collector := metrics.NewCollector(pool, metrics.Options{Logger: log})
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	q := queue.New(pool, queue.Options{Logger: log, OnAttempt: collector.ObserveAttempt})
	bpmEngine, err := engine.New(q, engine.Config{
		KindPrefix:   kindPrefix,
		History:      history,
		RulesHistory: rulesHistory,
		Logger:       log,
	}, processes, bpmgen.Queues(svc))
	if err != nil {
		return err
	}
	historyReader := engine.NewHistoryReader(pool, processes, bpmgen.Decisions)

	mux := http.NewServeMux()
	httpapi.Register(mux, bpmEngine, historyReader)
	httpapi.RegisterUserTasks(mux, engine.NewUserTaskService(pool, bpmEngine))
	httpapi.RegisterOperations(mux, engine.NewInstanceOperations(pool, bpmEngine))
	webui.Register(mux, historyReader)
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.Handle("GET /{$}", http.RedirectHandler("/ui/", http.StatusFound))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	q.Start(ctx)
	defer q.Stop()
	// очистка истории по сроку хранения: без -retention выключена
	cleaner := engine.NewRetention(pool, engine.RetentionConfig{After: retention, Logger: log})
	cleaner.Start(ctx)
	defer cleaner.Stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Info("demo started", "addr", addr, "console", "http://localhost"+addr+"/ui/")

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// envDuration - длительность из переменной окружения; не задана или не разбирается - fallback
func envDuration(name string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(os.Getenv(name)); err == nil {
		return value
	}
	return fallback
}

func flagFunc(value bool) func() bool {
	return func() bool { return value }
}
