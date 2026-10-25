// Демо-сервис go-bpm: процессы из examples/demo/process, JSON API и страница истории на одном порту.
//
//	go run ./examples/demo/cmd/demo -dsn postgres://postgres:postgres@localhost:5432/go_bpm?sslmode=disable
//
// Запуск процесса и история:
//
//	curl -XPOST localhost:8080/api/v1/processes/TestDecision/instances -d '{"variables":{"a":"some","b":"x"}}'
//	open http://localhost:8080/history/?instanceId=<id>
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

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/historyview"
	"github.com/nicolas-vp/go-bpm/httpapi"
	"github.com/nicolas-vp/go-bpm/migrations"
	"github.com/nicolas-vp/go-bpm/queue"
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
	)
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log, *dsn, *addr, *kindPrefix, *skipMigrations, engine.HistoryConfig{
		ToLog: flagFunc(*historyLog), ToDB: flagFunc(*historyDB),
	}, engine.HistoryConfig{
		ToLog: flagFunc(*rulesLog), ToDB: flagFunc(*rulesDB),
	}); err != nil {
		log.Error("demo", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, dsn, addr, kindPrefix string, skipMigrations bool, history, rulesHistory engine.HistoryConfig) error {
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
	q := queue.New(pool, queue.Options{Logger: log})
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
	historyview.Register(mux, historyReader)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	q.Start(ctx)
	defer q.Stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Info("demo started", "addr", addr, "history", "http://localhost"+addr+"/history/?instanceId=<id>")

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

func flagFunc(value bool) func() bool {
	return func() bool { return value }
}
