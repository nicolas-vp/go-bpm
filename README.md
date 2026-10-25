# go-bpm

BPM-движок на PostgreSQL для Go-сервисов. Процессы рисуются в BPMN/DMN (Camunda 8, zeebe), генератор
`go-bpm-gen` превращает их в типизированный Go-код, движок исполняет его на очереди задач в той же базе.

- Каждый шаг процесса — задача очереди; все его эффекты пишутся в одной транзакции с её завершением.
- Service task, задачи и скрипты, шлюзы (exclusive, parallel), подпроцессы и callActivity, таймеры, граничные события
  таймера и ошибки, решения DMN — сразу в Go, без интерпретатора выражений в рантайме.
- Задачи пользователей как в Camunda 7: исполнитель, кандидаты-пользователи и группы, очередь, взять в работу и выполнить через API.
- Экземпляры со статусом и бизнес-ключом, история в базе, консоль `/ui/`: процессы, запуск, поиск экземпляров, задачи
  и история экземпляра с диаграммой bpmn-js и сработавшими правилами DMN.
- Зависимости: `pgx/v5`, `google/uuid`, `golang.org/x/tools` (генератор).

## Документация

| | |
|---|---|
| [Быстрый старт](docs/quickstart.md) | демо за пять минут и свой сервис с нуля |
| [Принципы работы](docs/concepts.md) | BPMN и DMN, токен, шаги и транзакции, повторы, переменные |
| [Моделирование BPMN](docs/bpmn.md) | поддерживаемые элементы, настройки в Camunda Modeler, ограничения |
| [Решения DMN](docs/dmn.md) | таблицы решений, ячейки, hit policy, история правил |
| [Генератор кода](docs/generator.md) | запуск, раскладка, модель данных, методы и обработчики |
| [Разбор примеров](docs/examples.md) | все процессы демо: BPMN, код, запуск, эксперименты |
| [Запуск и эксплуатация](docs/running.md) | настройки, таблицы, масштабирование, мониторинг, упавшие экземпляры |
| [HTTP API и консоль](docs/api.md) | процессы, экземпляры, задачи пользователей, консоль `/ui/` |

## Встроить в сервис

```sh
go get github.com/nicolas-vp/go-bpm
go get -tool github.com/nicolas-vp/go-bpm/cmd/go-bpm-gen
```

BPMN и DMN кладутся в `process/` в корне модуля, генерация — в `internal/bpmgen`:

```go
// internal/gen/generate.go
package gen

//go:generate go tool go-bpm-gen
```

```go
pool, _ := pgxpool.New(ctx, dsn)
_ = migrations.Up(ctx, pool)

svc := &services.Services{} // ваши внешние сервисы для методов и обработчиков
q := queue.New(pool, queue.Options{})
on := func() bool { return true }
e, _ := engine.New(q, engine.Config{
	History:      engine.HistoryConfig{ToDB: on}, // экземпляры и история в базе
	RulesHistory: engine.HistoryConfig{ToDB: on}, // сработавшие правила DMN
}, bpmgen.Processes(svc), bpmgen.Queues(svc))
history := engine.NewHistoryReader(pool, bpmgen.Processes(svc), bpmgen.Decisions)

mux := http.NewServeMux()
httpapi.Register(mux, e, history)                                // /api/v1/processes, /api/v1/instances
httpapi.RegisterUserTasks(mux, engine.NewUserTaskService(pool, e)) // /api/v1/tasks
webui.Register(mux, history)                                     // /ui/ - консоль, /ui/history/?instanceId=<id> - история

q.Start(ctx)
defer q.Stop()
```

Рукописные части — модель данных `internal/bpmgen/model`, сервисы `internal/bpmgen/services`, обработчики
service task `internal/bpmgen/handlers` и методы задач `method_<id>.go`. Генератор создаёт для них заглушки и
никогда не перезаписывает. Пошагово — [Быстрый старт](docs/quickstart.md#свой-сервис-с-нуля); решения и
договорённости разработчиков движка — [docs/context.md](docs/context.md).

## Демо

```sh
createdb go_bpm
make run    # examples/demo, консоль http://localhost:8080/ui/
curl -XPOST localhost:8080/api/v1/processes/TestDecision/instances -d '{"variables":{"a":"some","b":"x"}}'
open "http://localhost:8080/ui/history/?instanceId=<id>"
```

## Разработка

```sh
make generate   # перегенерировать examples/demo из BPMN
make test       # с Postgres из GO_BPM_TEST_DSN (база очищается!), по умолчанию localhost/go_bpm_test
make test-unit  # без базы
make lint
```

| Каталог | Что там |
|---|---|
| `bpm/` | контракт между генератором, сгенерированным кодом и движком |
| `engine/` | движок |
| `queue/` | очередь задач на PostgreSQL |
| `migrations/` | схема БД |
| `httpapi/`, `webui/` | JSON API и консоль с историей экземпляров |
| `cmd/go-bpm-gen/`, `internal/generator/` | генератор |
| `examples/demo/` | демо-сервис |
