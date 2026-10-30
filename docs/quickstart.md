# Быстрый старт

Два сценария: запустить демо из репозитория и встроить движок в свой сервис с нуля. Оба проверены на чистой базе,
выводы команд ниже — настоящие.

## Что нужно

- Go 1.26 или новее (`go version`).
- PostgreSQL 13+ и пользователь, который может создавать таблицы. Локально проще всего в Docker:

  ```sh
  docker run -d --name go-bpm-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:17
  ```

- [Camunda Modeler](https://camunda.com/download/modeler/) — рисовать BPMN и DMN. Платформа при создании файла —
  **Camunda 8** (атрибуты `zeebe:*`). Подробнее — [Моделирование BPMN](bpmn.md#чем-рисовать).

## Демо за пять минут

В репозитории есть демо-сервис `examples/demo` с семью процессами: шлюзы и параллельные ветки, подпроцесс, таймеры
и ошибки, бизнес-правила DMN, задачи пользователей, multi-instance, ожидание сообщения.

```sh
git clone git@github.com:nicolas-vp/go-bpm.git
cd go-bpm
createdb -h localhost -U postgres go_bpm      # или: docker exec go-bpm-pg createdb -U postgres go_bpm
make run
```

`make run` применяет миграции и поднимает HTTP-сервер на `:8080`:

```
level=INFO msg="demo started" addr=:8080 console=http://localhost:8080/ui/
```

Другая база — через переменную: `make run GO_BPM_DSN='postgres://user:pass@host:5432/db?sslmode=disable'`.

### Посмотреть в консоли

Откройте <http://localhost:8080/ui/>.

1. **Процессы** — пять процессов демо с числом экземпляров по статусам. «Схема» показывает процесс.

   ![Вкладка «Процессы»: процессы демо, счётчики экземпляров и схема «Согласование заявки»](images/console-processes.png)

2. **Запуск** — выберите «Тестируем бизнес правило» (`TestDecision`), добавьте переменные `a` = `foo` и
   `b` = `bar` (тип «строка»), нажмите «Запустить». Под формой появится ссылка «История экземпляра».

   ![Вкладка «Запуск» (на снимке — «Согласование заявки»): процесс, бизнес-ключ и переменные строками](images/console-start.png)

3. В истории пройденные элементы закрашены. Клик по задаче «Бизнес правило» показывает переменные после неё
   (`c` = `correct foobar`).

   ![История экземпляра TestDecision: пройденные элементы и панель задачи бизнес-правила](images/history-decision.png)

   Кнопка «Показать сработавшие правила» открывает таблицу DMN с подсвеченным правилом:

   ![Окно сработавших правил: таблица Decision_1, сработало первое правило](images/history-rules.png)

4. **Задачи** — запустите «Согласование заявки» (`TestUserTask`) с переменной `initiator` = `alice`. Во вкладке
   «Задачи» введите пользователя `bob`, группы `managers` и нажмите «Показать»: задача «Согласовать заявку» в списке
   «Можно взять». «Взять» → «Выполнить» → результат `{"approved": true}` — экземпляр завершится.
   С `{"approved": false}` задача «Исправить заявку» уйдёт исполнителю `alice`.

   ![Вкладка «Задачи»: задача пользователя erin и очередь группы admins](images/console-tasks.png)

### То же через API

```sh
# запустить процесс с бизнес-ключом
curl -XPOST localhost:8080/api/v1/processes/TestDecision/instances \
  -d '{"variables":{"a":"foo","b":"bar"},"businessKey":"order-1"}'
# {"id":"01a17412-d14e-77d9-9651-a32ee9008420"}

# экземпляры процесса
curl 'localhost:8080/api/v1/instances?processName=TestDecision'

# история экземпляра: каждый пройденный элемент с переменными
curl localhost:8080/api/v1/instances/01a17412-d14e-77d9-9651-a32ee9008420/history
```

В логе сервиса видно каждый шаг и сработавшие правила:

```
bpm history: completed process="TestDecision" instance=01a17412-... element="Activity_0xzljrt" type=Method variables={"a":"foo",...,"c":"correct foobar"}
rules history: process="TestDecision" instance=01a17412-... element="Activity_0xzljrt" decision="Decision_1" rules=["DecisionRule_04e4bk8"] result="correct foobar"
```

Все процессы демо разобраны в [Разборе примеров](examples.md).

## Свой сервис с нуля

Сделаем сервис `orders` с процессом согласования заказа: проверить заказ, крупный заказ отдать на согласование
менеджеру, затем уведомить клиента.

![Процесс OrderApproval: проверить заказ, крупный — на согласование, затем уведомить клиента](images/order.svg)

### 1. Модуль и зависимости

```sh
mkdir orders && cd orders
go mod init example.com/orders
go get github.com/nicolas-vp/go-bpm@latest
go get -tool github.com/nicolas-vp/go-bpm/cmd/go-bpm-gen@latest
```

`go get -tool` записывает генератор в `go.mod` директивой `tool`: он будет той же версии, что и движок, и
запускается как `go tool go-bpm-gen`.

> Если в окружении задан корпоративный `GOPROXY`, а модуль берётся с GitHub:
> `GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org go get ...`

### 2. Директива генерации

Генератор по умолчанию читает BPMN и DMN из `process/` в корне модуля и пишет код в `internal/bpmgen/`. Директиву
`go:generate` можно положить в любой пакет, например:

```go
// internal/gen/generate.go
package gen

//go:generate go tool go-bpm-gen
```

### 3. Процесс

Нарисуйте процесс в Camunda Modeler (Camunda 8) и сохраните как `process/order.bpmn`:

| Элемент | Тип | id | Настройка в панели свойств |
|---|---|---|---|
| Проверить заказ | Task | `CheckOrder` | — |
| Крупный заказ? | Exclusive gateway | `IsBig` | default flow — ветка «нет» |
| ветка «да» | Sequence flow | `Flow_big` | Condition expression: `=amount > 1000` |
| Согласовать заказ | User task | `Approve` | Assignment → Candidate groups: `managers` |
| Уведомить клиента | Service task | `Notify` | Task definition → Type: `notifyClient`, Retries: `3` |

<details>
<summary>Тот же процесс в XML (без диаграммы)</summary>

```xml
<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
    id="Definitions_order" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="OrderApproval" name="Согласование заказа" isExecutable="true">
    <bpmn:startEvent id="Start">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Start" targetRef="CheckOrder" />
    <bpmn:task id="CheckOrder" name="Проверить заказ">
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:task>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="CheckOrder" targetRef="IsBig" />
    <bpmn:exclusiveGateway id="IsBig" name="Крупный заказ?" default="Flow_small">
      <bpmn:incoming>Flow_2</bpmn:incoming>
      <bpmn:outgoing>Flow_big</bpmn:outgoing>
      <bpmn:outgoing>Flow_small</bpmn:outgoing>
    </bpmn:exclusiveGateway>
    <bpmn:sequenceFlow id="Flow_big" name="да" sourceRef="IsBig" targetRef="Approve">
      <bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">=amount &gt; 1000</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_small" name="нет" sourceRef="IsBig" targetRef="Notify" />
    <bpmn:userTask id="Approve" name="Согласовать заказ">
      <bpmn:extensionElements>
        <zeebe:assignmentDefinition candidateGroups="managers" />
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_big</bpmn:incoming>
      <bpmn:outgoing>Flow_3</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Approve" targetRef="Notify" />
    <bpmn:serviceTask id="Notify" name="Уведомить клиента">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="notifyClient" retries="3" />
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_small</bpmn:incoming>
      <bpmn:incoming>Flow_3</bpmn:incoming>
      <bpmn:outgoing>Flow_4</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:sequenceFlow id="Flow_4" sourceRef="Notify" targetRef="End" />
    <bpmn:endEvent id="End">
      <bpmn:incoming>Flow_4</bpmn:incoming>
    </bpmn:endEvent>
  </bpmn:process>
</bpmn:definitions>
```

Без блока `bpmndi:BPMNDiagram` процесс исполняется, но консоль не нарисует схему — рисуйте в моделере.
`<bpmn:incoming>`/`<bpmn:outgoing>` внутри узлов моделер пишет всегда; в XML, написанном руками, их можно не писать —
генератор восстановит переходы по `sourceRef` и `targetRef`.
</details>

### 4. Первая генерация

```sh
go generate ./internal/gen
```

```
go-bpm-gen: .../orders/process -> .../orders/internal/bpmgen
переменных нет в модели данных model, добавьте поля:
	model.DataModel:
		Amount int `json:"amount"`
используются в:
	процесс OrderApproval: условие IsBig
```

Это ожидаемо. Генератор создал заготовки и остановился: условие шлюза использует переменную `amount`, а в модели
данных такого поля нет. Тип в подсказке выведен из условия (`> 1000` — целое), его можно поменять.

Что появилось:

```
process/bpmn-gen.go                               вшитые BPMN/DMN (сгенерирован)
internal/bpmgen/
  bpm-gen.go, queue-gen.go                        сборка процессов и очередей (сгенерированы)
  model/data_model.go                             DataModel — пишете вы
  services/services.go                            внешние зависимости методов — пишете вы
  handlers/notify_client.go                       обработчик service task — пишете вы
  order_approval/
    flow-gen.go                                   схема процесса (сгенерирован)
    method_check_order.go                         метод задачи «Проверить заказ» — пишете вы
```

Файлы с заголовком `// Code generated by go-bpm-gen. DO NOT EDIT.` генератор пересоздаёт каждый раз, остальные
создаёт один раз и больше не трогает.

### 5. Модель данных

```go
// internal/bpmgen/model/data_model.go
type DataModel struct {
	Amount   float64 `json:"amount"`
	Checked  bool    `json:"checked"`
	Notified bool    `json:"notified"`
}
```

Каждое поле — переменная процесса с именем из тега `json`. Подробнее — [Генератор → модель данных](generator.md#модель-данных).

### 6. Метод и обработчик

Метод задачи `CheckOrder` выполняется синхронно, в шаге процесса:

```go
// internal/bpmgen/order_approval/method_check_order.go
func CheckOrder(ctx context.Context, svc *services.Services, data *model.DataModel) error {
	if data.Amount <= 0 {
		return fmt.Errorf("сумма заказа должна быть положительной: %v", data.Amount)
	}
	data.Checked = true
	return nil
}
```

Обработчик service task `notifyClient` выполняется асинхронно, отдельной задачей очереди с повторами:

```go
// internal/bpmgen/handlers/notify_client.go
func NotifyClient(ctx context.Context, svc *services.Services, job bpm.Job, data *model.DataModel) error {
	slog.InfoContext(ctx, "уведомляем клиента", "instance", job.InstanceUid, "amount", data.Amount)
	data.Notified = true
	return nil
}
```

Перегенерируйте — теперь без ошибок (генератор ещё и собирает пакет, проверяя ваши методы):

```sh
go generate ./internal/gen
```

### 7. main.go

```go
// cmd/orders/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/httpapi"
	"github.com/nicolas-vp/go-bpm/migrations"
	"github.com/nicolas-vp/go-bpm/queue"
	"github.com/nicolas-vp/go-bpm/webui"

	"example.com/orders/internal/bpmgen"
	"example.com/orders/internal/bpmgen/services"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	// таблицы bpm_*: применяются недостающие миграции, безопасно при одновременном старте подов
	if err := migrations.Up(ctx, pool); err != nil {
		log.Fatal(err)
	}

	svc := &services.Services{} // клиенты внешних систем для методов и обработчиков
	processes := bpmgen.Processes(svc)
	on := func() bool { return true }

	q := queue.New(pool, queue.Options{})
	e, err := engine.New(q, engine.Config{
		History:      engine.HistoryConfig{ToDB: on}, // экземпляры и история элементов
		RulesHistory: engine.HistoryConfig{ToDB: on}, // сработавшие правила DMN
	}, processes, bpmgen.Queues(svc))
	if err != nil {
		log.Fatal(err)
	}
	history := engine.NewHistoryReader(pool, processes, bpmgen.Decisions)

	mux := http.NewServeMux()
	httpapi.Register(mux, e, history)                                  // процессы и экземпляры
	httpapi.RegisterUserTasks(mux, engine.NewUserTaskService(pool, e)) // задачи пользователей
	webui.Register(mux, history)                                       // консоль /ui/

	q.Start(ctx) // воркеры очереди: с этого момента процессы исполняются
	defer q.Stop()

	server := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()
	log.Println("консоль: http://localhost:8080/ui/")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
```

`History.ToDB` и `RulesHistory.ToDB` включены, чтобы экземпляры, история и сработавшие правила писались в базу:
без них консоли нечего показывать. По умолчанию всё выключено — см. [настройки истории](running.md#история).

```sh
go mod tidy
createdb orders
DATABASE_URL='postgres://postgres:postgres@localhost:5432/orders?sslmode=disable' go run ./cmd/orders
```

### 8. Проверка

```sh
curl -XPOST localhost:8080/api/v1/processes/OrderApproval/instances -d '{"variables":{"amount":500},"businessKey":"order-1"}'
curl -XPOST localhost:8080/api/v1/processes/OrderApproval/instances -d '{"variables":{"amount":5000},"businessKey":"order-2"}'
```

Маленький заказ сразу проходит до конца — в логе:

```
INFO уведомляем клиента instance=01a17411-e693-... amount=500
INFO process completed process=OrderApproval instance=01a17411-e693-...
```

Крупный ждёт менеджера:

```sh
curl 'localhost:8080/api/v1/tasks?candidateUser=bob&candidateGroup=managers'
# {"tasks":[{"task_uid":"01a17411-e6a2-...","element_id":"Approve","name":"Согласовать заказ",
#            "business_key":"order-2","candidate_groups":["managers"],"status":"created",...}]}

curl -XPOST localhost:8080/api/v1/tasks/01a17411-e6a2-.../claim -d '{"userId":"bob","groups":["managers"]}'      # 204
curl -XPOST localhost:8080/api/v1/tasks/01a17411-e6a2-.../complete -d '{"userId":"bob","variables":{"approved":true}}' # 204
```

История экземпляра `order-2` (`GET /api/v1/instances/{id}/history`), сокращённо:

```
completed Start       {"amount": 5000}
completed CheckOrder  {"amount": 5000, "checked": true, "notified": false}
completed IsBig
completed Flow_big
activated Approve     {"amount": 5000, "checked": true, "notified": false}
completed Approve     {"amount": 5000, "approved": true, "checked": true, "notified": false}
activated Notify      ...
completed Notify      {"amount": 5000, "approved": true, "checked": true, "notified": true}
completed End
```

Обратите внимание на `approved`: его нет в `DataModel`, но переменная сохранилась — движок не теряет переменные,
которых модель не знает.

А заказ с `amount = 0` упадёт в методе `CheckOrder`: шаг будет повторяться (`WARN queue: task will be retried`), пока
не кончатся попытки (10 попыток с растущей задержкой — около шести минут). Всё это время экземпляр виден в списке
со статусом `running`, потом — `failed`.
Что с этим делать — [Запуск и эксплуатация → упавшие экземпляры](running.md#упавшие-экземпляры).

## Дальше

- [Принципы работы](concepts.md) — как движок исполняет процесс и что гарантирует.
- [Моделирование BPMN](bpmn.md) — все поддерживаемые элементы и их настройки.
- [Генератор кода](generator.md) — что генерируется и как писать методы.
