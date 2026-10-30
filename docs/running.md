# Запуск и эксплуатация

Как собрать движок в сервисе, какие у него настройки, что лежит в базе и что делать, когда экземпляр упал.

## Docker

Демо-сервис собирается в образ `ghcr.io/nicolas-vp/go-bpm` (`Dockerfile` в корне: статический бинарник в
distroless, около 15 МБ). Его публикует CI (`.github/workflows/ci.yml`, job `image`) после зелёных тестов:

| Событие | Теги образа |
|---|---|
| push в `main` | `latest`, `sha-<коммит>` |
| тег `v1.2.3` | `1.2.3`, `1.2`, `sha-<коммит>` |
| pull request | образ собирается, но не публикуется |

Запуск с PostgreSQL — `compose.yaml` в корне:

```sh
cp .env.example .env          # задайте POSTGRES_PASSWORD (буквы и цифры)
docker compose up -d          # образ из ghcr.io; из исходников: docker compose up -d --build
docker compose logs -f go-bpm
```

Консоль — <http://localhost:8080/ui/>. Данные базы — в томе `pgdata`, переживают пересоздание контейнеров.

| Переменная `.env` | По умолчанию | Что это |
|---|---|---|
| `POSTGRES_PASSWORD` | — (обязательна) | пароль пользователя `go_bpm`; подставляется в строку подключения |
| `GO_BPM_TAG` | `latest` | версия образа |
| `GO_BPM_LISTEN` | `127.0.0.1:8080` | где слушать на хосте |

Порт по умолчанию открыт только на localhost: у консоли и API нет авторизации. Наружу их выставляйте через
reverse proxy с авторизацией (nginx, Caddy, Traefik). База наружу не публикуется вовсе.

Обновление до нового образа: `docker compose pull && docker compose up -d` — миграции применятся при старте.

### Безопасность поставки

- CI запускает `govulncheck` на последнем патче Go 1.26 (`make vulncheck`): сборка падает, если код вызывает
  функцию с известной уязвимостью в модуле или стандартной библиотеке.
- Опубликованный образ несёт аттестации SBOM (состав пакетов) и provenance (как и из какого коммита собран) —
  `docker buildx imagetools inspect ghcr.io/nicolas-vp/go-bpm:latest --format '{{ json .SBOM }}'`.
- Образ подписан `cosign` без ключей, через OIDC-токен GitHub Actions; запись подписи — в публичном журнале Rekor.
  Проверка:

  ```sh
  cosign verify ghcr.io/nicolas-vp/go-bpm:latest \
    --certificate-identity-regexp '^https://github.com/nicolas-vp/go-bpm/' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
  ```

### Выкладка на сервер из CI

Job `deploy` в `.github/workflows/ci.yml` после публикации образа из `main` заходит на сервер по SSH и вызывает
`deploy/deploy.sh`: тот обновляет `compose.yaml`, скачивает образ `sha-<коммит>`, пересоздаёт контейнер
приложения и удаляет старые образы. База и её том остаются. Подготовка сервера (Ubuntu с Docker):

```sh
apt-get install -y docker-compose-v2
install -d -m 750 /opt/go-bpm && install -m 750 deploy/deploy.sh /opt/go-bpm/   # скрипт и compose.yaml из репозитория
cp compose.yaml /opt/go-bpm/
printf 'POSTGRES_PASSWORD=%s\nGO_BPM_TAG=latest\nGO_BPM_LISTEN=127.0.0.1:8080\n' "$(openssl rand -hex 24)" > /opt/go-bpm/.env
chmod 600 /opt/go-bpm/.env
ssh-keygen -t ed25519 -N '' -f deploy_key   # ключ для CI
echo "command=\"/opt/go-bpm/deploy.sh\",restrict $(cat deploy_key.pub)" >> ~/.ssh/authorized_keys
```

Секреты репозитория (Settings → Secrets and variables → Actions): `DEPLOY_HOST` — адрес сервера,
`DEPLOY_SSH_KEY` — содержимое `deploy_key`, `DEPLOY_KNOWN_HOSTS` — вывод `ssh-keyscan -t ed25519 <адрес>`.
Ключу разрешено только вызвать `deploy.sh`. Откат на прошлую версию — `GO_BPM_TAG=sha-<коммит>` в
`/opt/go-bpm/.env` и `docker compose up -d` в этом каталоге.

Свой сервис на go-bpm собирается так же: замените в `Dockerfile` путь `./examples/demo/cmd/demo` на свой `main`.

## Сборка сервиса

Минимальный порядок вызовов:

```go
pool, _ := pgxpool.New(ctx, dsn)
_ = migrations.Up(ctx, pool)                         // 1. схема

svc := &services.Services{ /* зависимости */ }       // 2. сервисы для методов и обработчиков
processes := bpmgen.Processes(svc)

q := queue.New(pool, queue.Options{})                // 3. очередь
e, _ := engine.New(q, engine.Config{}, processes, bpmgen.Queues(svc)) // 4. движок — до q.Start

history := engine.NewHistoryReader(pool, processes, bpmgen.Decisions) // 5. чтение истории (по желанию)
tasks := engine.NewUserTaskService(pool, e)                           //    задачи пользователей (по желанию)

q.Start(ctx)                                         // 6. воркеры: процессы начинают исполняться
defer q.Stop()                                       //    при остановке — дождаться взятых задач
```

- `engine.New` регистрирует виды задач в очереди, поэтому вызывается до `q.Start`.
- `e.StartProcess` можно вызывать и до `q.Start`: задача старта запишется в базу и выполнится, когда воркеры
  запустятся.
- HTTP API и консоль — по желанию, см. [HTTP API и консоль](api.md). Полный пример — `examples/demo/cmd/demo/main.go`.

### Запуск процесса из кода

```go
id, err := e.StartProcess(ctx, "OrderApproval", map[string]any{
	"amount":   5000,
	"category": "vip",
}, engine.WithBusinessKey("order-42"))
```

- Возвращает id экземпляра сразу, процесс выполняется асинхронно воркерами очереди. Экземпляр виден в
  `bpm_process_instance` со статусом `running` сразу после вызова (при `History.ToDB`).
- Переменные — любые JSON-значения. Числа из JSON приходят как `float64` и раскладываются в поля модели нужного типа.
- Неизвестный процесс — `engine.ErrProcessNotFound`.

### Запуск в транзакции сервиса

`StartProcessTx` запускает процесс в вашей транзакции той же базы: процесс начнётся, только если она закоммитится.
Так заказ и процесс его обработки появляются атомарно — нет заказов без процесса и процессов без заказа:

```go
err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `insert into orders (id, amount) values ($1, $2)`, orderID, amount); err != nil {
		return err
	}
	_, err := e.StartProcessTx(ctx, tx, "OrderApproval", map[string]any{"amount": amount},
		engine.WithBusinessKey(orderID))
	return err
})
```

## База данных

### Миграции

`migrations.Up(ctx, pool)` применяет недостающие миграции:

- под `pg_advisory_lock` — несколько подов, стартующих одновременно, не мешают друг другу;
- каждую в своей транзакции, версии — в `bpm_schema_migrations`.

Если в сервисе свой инструмент миграций (goose, migrate, liquibase), возьмите SQL через `migrations.List()` и не
вызывайте `Up`. Файлы — `migrations/sql/NNNN_<имя>.sql`, выпущенные не меняются.

### Таблицы

| Таблица | Что хранит | Растёт |
|---|---|---|
| `bpm_queue` | задачи очереди: шаги, service task, таймеры | выполненные удаляются через `Retention` (7 дней), `failed` — никогда |
| `bpm_process_instance` | экземпляры: процесс, бизнес-ключ, родитель, статус (`running`, `completed`, `failed`, `cancelled`) | строка на экземпляр; удаляет `engine.Retention`, если включена |
| `bpm_history` | история элементов | строка на каждый элемент и переход; удаляет `engine.Retention` |
| `bpm_rules_history` | вызовы решений DMN | строка на вызов; удаляет `engine.Retention` |
| `bpm_user_task` | задачи пользователей | строка на каждый приход токена в user task; удаляет `engine.Retention` |
| `bpm_parallel_sync` | счётчики слияния параллельных веток | временные |
| `bpm_wait` | гонка задачи (service task, user task) и её граничного таймера | временные |
| `bpm_message_subscription` | токены, которые ждут сообщение | временные: удаляются, когда сообщение пришло или экземпляр отменён |
| `bpm_message` | сообщения, которые ждут подписку (`WithMessageTTL`) | временные: забирает подписка, просроченные удаляет `engine.Retention` |
| `bpm_schema_migrations` | применённые миграции | |

### Очистка истории

По умолчанию история и экземпляры не удаляются — это ваши данные. Очистку по сроку хранения включает
`engine.Retention`:

```go
retention := engine.NewRetention(pool, engine.RetentionConfig{After: 90 * 24 * time.Hour, Logger: log})
retention.Start(ctx)
defer retention.Stop()
```

| Поле | По умолчанию | Что это |
|---|---|---|
| `After` | 0 — выключена | сколько хранить экземпляр после завершения или отмены, от последнего шага (`updated_at`) |
| `Interval` | 1m | пауза между проходами; пока удаляются полные пачки, следующая идёт сразу |
| `BatchSize` | 100 | сколько корневых экземпляров удаляется в одной транзакции |
| `Logger` | `slog.Default()` | ошибки очистки пишутся предупреждением |

- Удаляются корневые экземпляры `completed` и `cancelled` целиком, деревом: встроенные подпроцессы, callActivity,
  их `bpm_history`, `bpm_rules_history` и `bpm_user_task`. Дочерний экземпляр не удаляется, пока выполняется корень.
- `running` и `failed` не удаляются никогда: упавший экземпляр ждёт разбора.
- Несколько подов чистят одновременно, не мешая друг другу: корни берутся `for update skip locked`.
- Чистить по своему расписанию (cron, отдельная задача) можно без `Start`: `DeleteExpired(ctx)` удаляет одну пачку
  и возвращает число удалённых корней.
- Заодно удаляются сообщения `bpm_message`, которые не дождались подписки за свой ttl. Без очистки они остаются
  в таблице, но подпискам не достаются.
- Задачи `bpm_queue` чистит очередь, см. `queue.Options.Retention`.

## Настройки движка

`engine.Config`:

| Поле | По умолчанию | Что это |
|---|---|---|
| `KindPrefix` | `"bpm"` | префикс видов задач в очереди: `<prefix>.system`, `<prefix>.task.<jobType>` |
| `SystemWorkers` | 4 | сколько шагов процессов (с методами) выполняется одновременно в одном экземпляре сервиса |
| `SystemMaxAttempts` | 10 | попыток у шага процесса, прежде чем экземпляр станет `failed` |
| `AttemptTimeout` | 30s | таймаут одной попытки задачи (`ctx` метода и обработчика) |
| `RetryDelay` | 1s | первая задержка повтора, дальше удваивается |
| `TaskWorkers` | 1 | сколько задач **одного jobType** выполняется одновременно в одном экземпляре сервиса |
| `History` | выключена | история элементов и экземпляры, см. ниже |
| `RulesHistory` | выключена | история правил DMN |
| `Hooks` | нет | точки подключения: трассировка, логи, свои метрики, см. ниже |
| `Logger` | `slog.Default()` | логгер движка |

`queue.Options`:

| Поле | По умолчанию | Что это |
|---|---|---|
| `PollInterval` | 1s (±25%) | как часто опрашивать таблицу: нужно отложенным задачам (таймеры, повторы) |
| `Retention` | 7 дней | сколько хранить выполненные и отменённые задачи; отрицательное — не удалять |
| `OnAttempt` | nil | вызывается после каждой попытки задачи с исходом и временем, для метрик (`metrics.Collector.ObserveAttempt`) |
| `Logger` | `slog.Default()` | логгер очереди |

Задержка повтора начинается с `Config.RetryDelay`, удваивается с каждой попыткой со случайной половиной (equal
jitter) и не превышает 5 минут.

Задачи без задержки будят воркеры сразу через `LISTEN/NOTIFY`, поэтому шаги проходят за миллисекунды. Опрос нужен
только отложенным задачам: таймер срабатывает с точностью до `PollInterval`.

### Хуки

`engine.Hooks` — функции, которые движок вызывает по ходу процессов. Не заданы — не вызываются.

| Хук | Когда | Для чего |
|---|---|---|
| `OnElement(ctx, HistoryRecord)` | событие элемента: `activated`, `completed`, `terminated` — те же, что в истории, но независимо от `History` | свой журнал, уведомления, события в шину |
| `WrapCall(ctx, Call, next)` | вокруг вызова кода сервиса: метода task, scriptTask, businessRuleTask и обработчика service task | span OpenTelemetry, логи с контекстом, время вызовов. Должен вызвать `next` и вернуть его ошибку |
| `OnInstanceFailed(ctx, InstanceFailure)` | у задачи кончились попытки, экземпляр стал `failed` | алерт, тикет в поддержку |

```go
tracer := otel.Tracer("bpm")
engine.Config{Hooks: engine.Hooks{
	WrapCall: func(ctx context.Context, call engine.Call, next func(context.Context) error) error {
		ctx, span := tracer.Start(ctx, call.ProcessName+"/"+call.ElementId)
		defer span.End()
		span.SetAttributes(attribute.String("bpm.instance", call.InstanceUid.String()), attribute.Int("bpm.attempt", call.Attempt))
		err := next(ctx)
		if err != nil {
			span.RecordError(err)
		}
		return err
	},
}}
```

- Хуки вызываются синхронно, в воркере очереди: долгий хук задерживает шаг.
- `OnElement` и `OnInstanceFailed` вызываются в транзакции шага. Если шаг откатится и повторится, событие придёт ещё
  раз. Для уведомлений, которые нельзя задвоить, пишите в свою таблицу через ту же базу или делайте их идемпотентными.
- `Call.Attempt` — номер попытки задачи очереди. Метод выполняется в шаге предыдущей задачи (например, service task
  перед ним), и его попытки — попытки этой задачи. По `Attempt`, `LastAttempt` и ошибке `next` видны повторы.

### История

```go
engine.Config{
	History:      engine.HistoryConfig{ToLog: on, ToDB: on},
	RulesHistory: engine.HistoryConfig{ToLog: off, ToDB: on},
}
```

- `ToLog` — строка в stdout на каждый элемент:
  `bpm history: completed process="TestDecision" instance=... element="Activity_0xzljrt" type=Method variables={...}`.
- `ToDB` — запись в `bpm_history` в транзакции шага. **Экземпляры (`bpm_process_instance`) пишутся тоже только при
  `History.ToDB`**: без него консоль и поиск экземпляров пусты. Задачи пользователей пишутся всегда.
- Флаги — функции `func() bool`: читаются на каждый элемент, поэтому их можно переключать на лету (из конфига,
  фичефлага).
- Переменные пишутся только у старта и задач, у переходов и шлюзов — `NULL`, чтобы история не росла снимком на
  каждый переход.
- При повторе шага строки в логе могут задвоиться (вывод не откатывается), в базе — нет.

Демо управляет историей флагами: `-history-log`, `-history-db`, `-rules-log`, `-rules-db` (все включены по
умолчанию).

### Флаги демо

```sh
go run ./examples/demo/cmd/demo -help
```

| Флаг | Переменная | По умолчанию |
|---|---|---|
| `-dsn` | `GO_BPM_DSN` | `postgres://postgres:postgres@localhost:5432/go_bpm?sslmode=disable` |
| `-addr` | `GO_BPM_ADDR` | `:8080` |
| `-kind-prefix` | | `bpm` |
| `-history-log`, `-history-db`, `-rules-log`, `-rules-db` | | `true` |
| `-skip-migrations` | | `false` |
| `-retention` | `GO_BPM_RETENTION` | `0` — история не удаляется; например `720h` |

## Масштабирование

- **Несколько экземпляров сервиса на одной базе** — штатный режим. Воркеры берут задачи через
  `select ... for update skip locked`, одна задача достаётся одному воркеру.
- **Аренда задачи.** Взятая задача арендована на `AttemptTimeout + 10s`. Если под умер с задачей, по истечении
  аренды её возьмёт другой. Поэтому обработчики и методы должны переживать повтор.
- **Пропускная способность** растёт с `SystemWorkers` (шаги процессов), `TaskWorkers` (обработчики jobType) и числом
  подов. Пул соединений `pgxpool` должен быть больше суммы воркеров плюс одно соединение под `LISTEN`.
- **Долгие методы** занимают системный воркер на всё время. Всё, что дольше сотни миллисекунд или ходит в сеть, —
  кандидат в service task.

### Несколько версий процессов на одной базе

Токен хранит id текущего элемента. Если новая версия сервиса удаляет элементы, на которых стоят токены, такие
экземпляры упадут. Варианты:

1. **Совместимые изменения** (добавить элементы, поменять условия и код) — просто деплой.
2. **Удалить элемент** — сначала выкатить версию, куда токены больше не приходят, дождаться, пока старые экземпляры
   его пройдут, затем удалить.
3. **Несовместимая версия** — запустить новую версию сервиса с другим `KindPrefix` (`bpm2`). Старая версия
   доживает свои экземпляры на префиксе `bpm`, новая берёт только `bpm2`. Таблицы истории и экземпляров общие.

### Остановка

`q.Stop()` перестаёт выдавать задачи и ждёт, пока воркеры доделают взятые. Обработчики получают ctx без отмены по
сигналу, ограниченный `AttemptTimeout`. Останавливайте очередь до закрытия пула и после остановки HTTP-сервера.

## Мониторинг

### Метрики Prometheus

Пакет `metrics` — `prometheus.Collector` для реестра сервиса (`prometheus/client_golang`):

```go
collector := metrics.NewCollector(pool, metrics.Options{Logger: log})
prometheus.MustRegister(collector)
q := queue.New(pool, queue.Options{OnAttempt: collector.ObserveAttempt})
// ...
mux.Handle("GET /metrics", promhttp.Handler())
```

| Метрика | Метки | Что это |
|---|---|---|
| `bpm_queue_tasks` | `kind`, `status` | незавершённые задачи очереди: `pending` (ждут или выполняются), `failed` |
| `bpm_queue_lag_seconds` | `kind` | сколько ждёт самая старая задача, которую уже можно выдать: воркеры не успевают |
| `bpm_queue_attempt_duration_seconds` | `kind`, `result` | гистограмма попыток задач этого пода; `result` — `done`, `retry`, `failed`, `cancelled`, `lost` |
| `bpm_instances` | `process`, `status` | экземпляры `running` и `failed`; завершённые не считаются, их число растёт без конца |
| `bpm_user_tasks_open` | `process` | открытые задачи пользователей |
| `bpm_scrape_success` | | 1 — метрики из базы прочитаны, 0 — запрос упал (ошибка в логе) |

- Всё, кроме гистограммы попыток, читается запросами к базе при каждом опросе, поэтому одинаково на всех подах.
  В PromQL берите `max by (...)`, а не `sum`. Гистограмма — своя у каждого пода, её суммируйте.
- Ошибка базы не ломает `/metrics` сервиса: метрики движка пропускаются, `bpm_scrape_success` становится 0.
- `Options`: `Namespace` (по умолчанию `bpm`), `Timeout` на запросы одного опроса (5s), `Buckets` гистограммы.
- Демо отдаёт метрики на `GET /metrics` вместе с метриками рантайма Go.

Что алертить:

```promql
max by (kind) (bpm_queue_tasks{status="failed"}) > 0        # упавшие задачи
max by (kind) (bpm_queue_lag_seconds) > 60                   # воркеры не успевают
sum by (kind) (rate(bpm_queue_attempt_duration_seconds_count{result="retry"}[5m])) > 0.1
max(bpm_scrape_success) == 0                                 # метрики из базы не читаются
```

### Запросы к базе

То же и подробнее видно запросами к базе. Глубина очереди по видам:

```sql
select kind, status, count(*), min(run_at) as oldest
from bpm_queue
where status in ('pending', 'failed')
group by kind, status
order by kind, status;
```

Задачи, которые сейчас повторяются:

```sql
select id, kind, attempt, run_at, left(last_error, 200) as error
from bpm_queue
where status = 'pending' and attempt > 0
order by run_at;
```

Экземпляры по статусам:

```sql
select process_name, status, count(*)
from bpm_process_instance
group by 1, 2
order by 1, 2;
```

То же есть в консоли: вкладка «Процессы» показывает число выполняющихся, завершённых и упавших экземпляров каждого
процесса, а `GET /api/v1/processes` отдаёт эти счётчики в JSON.

## Упавшие экземпляры

Найти их проще всего в консоли: вкладка «Экземпляры», статус «упал» (или число в колонке «Упали» на вкладке
«Процессы»):

![Вкладка «Экземпляры» с фильтром «упал»: экземпляр TestBpmProcess order-1007](images/console-failed.png)

Экземпляр становится `failed`, когда у его задачи очереди кончились попытки. Задача остаётся в `bpm_queue` со
статусом `failed` и текстом ошибки:

```sql
select id, kind, attempt, last_error,
       coalesce(payload -> 'message' ->> 'InstanceUid', payload ->> 'InstanceUid') as instance_uid,
       coalesce(payload -> 'message' ->> 'CurrentPosition', payload ->> 'CurrentPosition') as element
from bpm_queue
where status = 'failed'
order by id desc;
```

```
 id |    kind    | attempt |                                   last_error
----+------------+---------+---------------------------------------------------------------------------------
  4 | bpm.system |      10 | process "OrderApproval": element "CheckOrder": сумма заказа должна быть положительной: 0
```

У системных задач токен — в `payload -> 'message'`, у задач service task — в самом `payload`. В токене переменные
(`Variables`), процесс, текущий элемент.

### Перезапуск и отмена

1. Устраните причину: выкатите исправленный код или поправьте данные.
2. Откройте историю экземпляра: над схемой — упавшие задачи с текстом ошибки. «Повторить» возвращает задачу в
   очередь с первой попытки, экземпляр и его родители снова `running`. «Исправить переменные…» — то же, но
   с переменными токена, исправленными в окне.
3. Если экземпляр не нужен, «Отменить экземпляр»: снимаются его задачи очереди и задачи пользователей вместе
   с дочерними экземплярами, статус — `cancelled`. Отменяется только корневой экземпляр.

То же через API (`GET /api/v1/instances/{id}/failures`, `POST /api/v1/failures/{taskId}/retry`,
`POST /api/v1/instances/{id}/cancel`, см. [API](api.md#упавшие-задачи-и-отмена-экземпляров)) и из Go —
`engine.NewInstanceOperations(pool, e)`.

Без консоли — тем же SQL, что делает `Retry`, в одной транзакции:

```sql
begin;
update bpm_queue
set status = 'pending', attempt = 0, run_at = now(), last_error = null, updated_at = now()
where id = 4 and status = 'failed';
update bpm_process_instance
set status = 'running', updated_at = now()
where instance_uid = '01a17411-e6a3-73b7-a4cb-18dd2f5855b4' and status = 'failed';
commit;
```

Переменные в токене правятся тем же `update`
(`payload = jsonb_set(payload, '{message,Variables,amount}', '700')`; у задач service task токен — сам `payload`),
а родителям упавшего дочернего экземпляра нужно вернуть `running` по цепочке `parent_instance_uid`.

## Тесты

### Тесты своих процессов

- **Методы и решения** — обычные юнит-тесты на пакет процесса: вызвать функцию с `*model.DataModel`, проверить поля.
  Решения DMN — неэкспортируемые функции `decide<Id>`, их тестируйте из того же пакета.
- **Процесс целиком** — интеграционный тест на настоящей базе: поднять очередь и движок, как в `main.go`,
  запустить процесс и дождаться статуса экземпляра через `HistoryReader.GetInstance`. Образец —
  `engine/e2e_test.go` и `engine/user_tasks_e2e_test.go` в репозитории.

### Тесты репозитория go-bpm

```sh
make test       # всё, включая интеграционные тесты на Postgres из GO_BPM_TEST_DSN — база очищается!
make test-unit  # без базы: интеграционные тесты пропускаются
make lint       # go vet и gofmt
make vulncheck  # govulncheck: известные уязвимости в вызываемом коде модулей и стандартной библиотеки
make generate   # перегенерировать демо после правки шаблонов генератора
```

`GO_BPM_TEST_DSN` по умолчанию — `postgres://postgres:postgres@localhost:5432/go_bpm_test?sslmode=disable`.
Не направляйте его на базу с данными.
