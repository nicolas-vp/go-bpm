# Контекст проекта go-bpm

Сводка решений и договорённостей по BPM-движку на PostgreSQL и генератору кода из BPMN.
Актуально на 2026-10-25. История до переезда — проект pg-bpm (три отдельных модуля), см. «Откуда пришли».

## Один модуль

Репозиторий `github.com/nicolas-vp/go-bpm` — один Go-модуль, в нём движок, генератор и контракт между ними:

| Пакет | Что это |
|---|---|
| `bpm` | Контракт: модели, на которые ссылается сгенерированный код и которые исполняет движок (бывший `pg-bpm-lib`) |
| `engine` | Движок: исполняет процессы на очереди задач (бывший `pkg/use_cases` движка pg-bpm) |
| `queue` | Очередь задач на PostgreSQL, таблица `bpm_queue` (замена внешней библиотеки очереди) |
| `migrations` | Схема БД: вшитые SQL-файлы и `Up(ctx, pool)` |
| `httpapi` | JSON API: старт процесса, поиск экземпляров, история экземпляра, задачи пользователей |
| `webui` | Консоль `/ui/`: процессы, запуск, поиск экземпляров, задачи пользователей; история экземпляра `/ui/history/` — диаграмма bpmn-js с пройденными элементами, таблицы DMN (бывший пакет `historyview`) |
| `cmd/go-bpm-gen` | Генератор Go-кода из BPMN/DMN (бывший `pg-bpm-gen`) |
| `internal/generator/...` | Внутренности генератора: `codegen` (бывший `queue-generator`), `templates`, `parser`, `models`, `utils`, `file` |
| `internal/pgtest` | Postgres для интеграционных тестов (`GO_BPM_TEST_DSN`) |
| `examples/demo` | Демо-сервис: BPMN в `process/`, сгенерированный код в `bpmgen/`, `cmd/demo/main.go` |

**Почему один модуль, а не три.** Целевой сценарий — движок как библиотека, которую встраивают сервисы. Такому сервису
нужны все три части сразу: движок (`engine`), контракт (его импортирует сгенерированный код) и генератор (`tool`).
Отдельные модули давали только цепочку релизов «lib → тег → gen → тег → движок» и риск, что шаблоны генератора
разойдутся с контрактом. В одном модуле версия одна: шаблоны, контракт и движок всегда согласованы и проверяются
одними тестами. Вынести контракт в свой модуль стоит, только если появится потребитель, которому нужен
сгенерированный код без движка (например, другой исполнитель процессов).

**Зависимости модуля:** `pgx/v5`, `google/uuid`, `golang.org/x/tools` и `x/mod` (генератор). Больше ничего: ни корпоративного фреймворка,
ни gRPC, ни chi, логирование — `log/slog`, HTTP — `net/http` (маршруты Go 1.22).

**Версия Go — 1.26.** Её требует `golang.org/x/tools v0.52`. `x/tools` нужен генератору ради `imports.Process`
(goimports): скрипты BPMN без `resultVariable` — код Go и могут использовать пакеты (`fmt`) без явного импорта.
Директива `go` — без патча (`1.26.0`), иначе `GOTOOLCHAIN` попытается скачать тулчейн.

## Генерация

### Как запускать

В демо (из корня репозитория): `make generate` или `go generate ./examples/demo` — директива в `examples/demo/generate.go`
с явными `-process process -output bpmgen`.

В сервисе, который встраивает движок:

```
go get -tool github.com/nicolas-vp/go-bpm/cmd/go-bpm-gen
# internal/gen/generate.go:  //go:generate go tool go-bpm-gen
go generate ./internal/gen
```

- **Пути по умолчанию.** `process/` и `internal/bpmgen` отсчитываются от корня модуля (ближайший `go.mod`), поэтому флаги не нужны. Явные `-process`/`-output` считаются от текущей папки.
- **Проверка сборки.** После генерации выполняется `go build ./...` папки вывода (флаг `-check`, по умолчанию включён). Так при генерации ловятся опечатки в скриптах и условиях BPMN, рукописные методы с неверной сигнатурой и расхождения шаблонов с контрактом `bpm`. Генератор вызывает `$GOROOT/bin/go`, если `GOROOT` задан (IDE часто задаёт `GOROOT` одного SDK, а в `PATH` первым стоит другой `go`).
- **Генерировать только в целевом проекте, не копировать.** Путь модуля для импортов генератор берёт из `go.mod` того проекта, куда пишет.
- В CI проверяется, что сгенерированный код демо совпадает с BPMN (`make generate && git diff --exit-code`).

### Раскладка: один Go-пакет на процесс

```
process/                        BPMN и DMN (обходится рекурсивно, подпапки допустимы)
  bpmn-gen.go                   go:embed BPMN- и DMN-файлов этой папки: map BPMN и DMN по пути файла
internal/bpmgen/                (в демо - examples/demo/bpmgen/)
  bpm-gen.go                    func Processes(svc) map[process id]bpm.Process, Decisions (XML DMN по id решения)
  queue-gen.go                  func Queues(svc) — jobType всех процессов, Retries и Handler: bpm.Handler(svc, handlers.DoSomeAsync)
                                (bpm-gen.go и queue-gen.go — единственные файлы с путём модуля)
  handlers/                     package handlers
    <job_type>.go               обработчик jobType — заглушка, ПИШЕТСЯ РУКАМИ; один на jobType всех процессов
  model/data_model.go           DataModel — жёсткая модель данных процессов; создаётся пустой один раз, ПИШЕТСЯ РУКАМИ
  services/services.go          Services — внешние сервисы; создаётся пустой один раз, ПИШЕТСЯ РУКАМИ, передаётся при старте
  test_bpm_process/             package test_bpm_process (имя папки = snake имени процесса, иначе id)
    flow-gen.go                 func Deployment(svc) — flow процесса и его подпроцессов
    cond_<id>.go                условие шлюза на модели данных, неэкспортируемая функция gateway1nptppd(data *model.DataModel)
    method_<id>.go              scriptTask и businessRuleTask — генерится; task — заглушка, ПИШЕТСЯ РУКАМИ
    decision_<id решения>.go    решение DMN, которое вызывают задачи бизнес-правил процесса
```

- **Почему один пакет на процесс.** В Go нет относительных импортов. Условия и методы лежат в том же пакете, что и flow, поэтому `flow-gen.go` ссылается на них без импорта. Из целевого проекта он импортирует только пакет `services`.
- **Имя корневого пакета** — по имени папки вывода: `bpmgen` → `package bpmgen` (папка с дефисом дала бы `bpm_gen`).
- **`process/bpmn-gen.go`** — map `BPMN` с BPMN-файлами по пути, вшитыми через `go:embed` без копирования. Используется страницей истории, `bpm-gen.go` берёт оттуда `Process.BPMN`. Файл лежит в самой `process/`, потому что `go:embed` не берёт файлы выше своего пакета. Поэтому генератор читает из `process/` только `*.bpmn` и `*.dmn`, а папка должна быть внутри модуля.
- **Сгенерированные файлы помечены заголовком** `// Code generated by go-bpm-gen. DO NOT EDIT.`. Перед генерацией они удаляются, поэтому не остаются условия и скрипты, которых больше нет в BPMN. Файлы без заголовка, то есть рукописные методы, генератор не трогает и не перезаписывает. Файлы со старым заголовком `pg-bpm-gen` генератор считает рукописными — при переносе проекта со старого генератора замените заголовок (так сделано с демо).
- **Модель данных и сервисы.** `model/` и `services/` общие для всех процессов, как `handlers/`. Генератор создаёт файл, только если в папке нет ни одного `.go`, поэтому модель можно разнести по нескольким файлам.
  - **Сервисы участвуют в старте:** `Processes(svc)`, `Queues(svc)` и `Deployment(svc)` каждого процесса — функции. Они замыкают сервисы в `bpm.Method(svc, X)` у всех методов (task, scriptTask, businessRuleTask) и `bpm.Handler(svc, handlers.X)`. Типы проверяет компилятор, движок о сервисах не знает. Поэтому путь модуля (пакет `services`) есть и в `flow-gen.go`, и в заглушках методов.
  - **Жёсткая модель** (`model_resolver.go`): любая переменная, которую используют условия, скрипты и решения DMN, должна быть полем `DataModel`. Переменная `a` — поле с тегом `json:"a"` (без тега — поле с именем `a`, совпадение точное, как при записи `encoding/json`). Путь `client.account.number` — поля вложенных структур пакета `model`: `data.Client.Account.Number`; указатели на структуры разыменовываются Go сами (nil — паника). Если путь дошёл до типа не из модели (`time.Time`), остаток пишется как есть: `created.Year()` → `data.Created.Year()`. Поля встроенных структур без тега поднимаются, поле верхнего уровня важнее.
  - **Недостающие поля.** Генератор читает рукописную модель через `go/parser` и не правит её. Недостающие поля собираются со всей генерации (`modelGaps`), генерация останавливается одной ошибкой: поля по структурам с типом по месту использования (``X float64 `json:"x"` ``, вложенный — ``Card Card `json:"card"` // структура модели``) и список, где они используются.
  - **Одна модель на все процессы**, поэтому переменная с одним именем должна иметь один тип во всех процессах (из-за этого в шлюзе `TestBpmProcess` `a` переименована в `amount`).
  - Поле без `omitempty` пишет нулевое значение в переменную процесса после каждого метода; с `omitempty` нулевое значение (например, `false`) переменную не меняет.
- **Удалённые процессы.** Пакет процесса, которого больше нет в BPMN, удаляется (`stale.go`). Если в нём есть рукописные файлы, папка остаётся, а генератор пишет предупреждение.
- **Конфликты имён.** Имена функций получаются из id элементов и jobType. Генератор останавливается с ошибкой, если два id в процессе дают одно имя или имя совпадает с `Deployment`, `svc`, `services` или `model` (`reservedNames`), а также если два jobType дают одно имя обработчика. Процесс не может называться `handlers`, `model` или `services` (`reservedDirs`) — папки заняты.
- **Правило: один jobType может использоваться в нескольких процессах.** Вид задач в очереди и обработчик у jobType одни на все процессы, поэтому очереди и обработчики не принадлежат процессу:
  - `queue-gen.go` в корне: service task всех процессов объединяются по jobType с максимальным `Retries`;
  - обработчики лежат в общем пакете `handlers/`, какой процесс и элемент вызвали обработчик — в `job.ProcessName`, `job.ElementId`;
  - из целевого проекта пакет процесса импортирует только `services` и `model`.
- **Обработчики пишутся руками.** Генератор только создаёт недостающие заглушки; обработчики jobType, которого больше нет в BPMN, не удаляются.
- Миграция со старых раскладок pg-bpm-gen (`conditions/`, `methods/`, `handler_<jobType>.go`, `deployment-gen.go`) удалена вместе с `legacy.go`: в новом продукте таких проектов нет.

### Проверка процесса (`codegen/validate.go`)

`prepareProcess` в начале `ParseFromProcess`: всё, что раньше генерировалось молча и роняло экземпляр в рантайме, когда до этого доходил токен, — ошибка генерации.

- **Неизвестные элементы.** У `bpmn.Process` (он же встроенный подпроцесс) поле `Other []Element xml:",any"` ловит всё, что не разобрано отдельно. Пропускаются только неисполняемые (`ignoredElements`: документация, `extensionElements`, лейны, аннотации, ассоциации, группы, объекты данных), остальное (inclusive/event-based/complex gateway, manual/send/receive task, throw events, transaction, ad-hoc) — ошибка. Раньше такой элемент не попадал во flow, а переход в него оставался: `element not found` в рантайме.
- **Определения событий** у start и end event (`OtherDefinitions`): таймер, сообщение, terminate, error — ошибка; раньше исполнялись как обычные. Ровно один start event у процесса и подпроцесса (раньше `GetStartingElement` брал первый или паниковал).
- **Циклы** (`multiInstanceLoopCharacteristics`, `standardLoopCharacteristics`) у задач, callActivity и подпроцесса — ошибка; раньше задача выполнялась один раз. Для этого у задач `Other []Element xml:",any"`.
- **Вложенный подпроцесс** — ошибка: `GenerateQueue` разворачивает только один уровень `SubProcessList`, внутренний не генерировался.
- **Переходы** (`linkFlows`): пустые `<bpmn:incoming>`/`<bpmn:outgoing>` заполняются по `sourceRef`/`targetRef` sequence flow в порядке файла (раньше BPMN, написанный руками, генерировался без переходов). Заданные должны совпадать с sequence flow как множество, порядок берётся из элемента — по нему проверяются условия шлюза, поэтому сгенерированный код моделерных файлов не меняется. Sequence flow к несуществующему элементу — ошибка.
- **Ветвления** (`checkBranches`): `MapOutgoings` превращал несколько выходов любого элемента в выбор по условиям — у задачи с двумя переходами без условий шаг падал. Теперь: несколько выходов — только у шлюзов, подпроцесса и callActivity (у двух последних — разветвление, как раньше); условие на переходе — только из exclusive gateway; у exclusive gateway каждый выход с условием или default, default должен выходить из шлюза.
- **Parallel gateway**, который и сливает, и разветвляет, теперь `SyncQueue` с `Straight` во все выходы; раньше уходил в выбор по условиям без условий.
- Проверено, что все пять демо-процессов проходят без изменений сгенерированного кода; тесты — `validate_test.go`.

### Что из BPMN во что превращается

| Элемент BPMN | ActivateType | Что генерируется |
|---|---|---|
| `serviceTask` (zeebe:taskDefinition type) | `Queue` | запись в корневом `queue-gen.go` со ссылкой `Handler`; заглушка `handlers/<jobType>.go`, пишется руками |
| `task` (обычная задача) | `Method` | заглушка `method_<id>.go`, пишется руками |
| `scriptTask` (zeebe:script) | `Method` | `method_<id>.go` с кодом скрипта на модели данных; при `resultVariable` результат пишется в поле модели |
| exclusive gateway с условиями | `CompleteType = Script` | `cond_<id>.go`: if-ветки по порядку, затем default flow |
| parallel gateway: разветвление (1 вход) | `Flow` | токен уходит во все исходящие flow |
| parallel gateway: слияние (>1 входа) | `SyncQueue` | `ParallelSyncQuantity` = число входящих веток |
| `subProcess` (встроенный) | `StartSubprocess` | flow `<process>.<subprocess>`; родитель ждёт его завершения |
| `intermediateCatchEvent` с таймером | `Timer` | `Expression.Timer`; длительность только `timeDuration` в ISO 8601 (`PT30S`, `PT5M`, `P1DT2H`), без лет и месяцев |
| `boundaryEvent` таймер или ошибка | `Boundary` | событие — отдельный элемент, по нему токен уходит дальше; у задачи `Expression.Boundaries` (`bpm.Boundary`: тип, `Timer`, `ErrorCode` из `bpmn:error`, пусто — любая ошибка) |
| `businessRuleTask` (zeebe:calledDecision) | `Method` | `method_<id>.go` вызывает решение и пишет результат в `resultVariable`; решение — `decision_<id решения>.go`, см. «Решения DMN» |
| `userTask` | `UserTask` | `Expression.UserTask`: название, исполнитель, кандидаты, форма; см. «Задачи пользователей» |
| `callActivity` (zeebe:calledElement) | `CallActivity` | отдельный экземпляр процесса `ProcessId`, родитель ждёт его End. Дочерний процесс получает копию переменных родителя. При `propagateAllChildVariables` (по умолчанию true) его переменные возвращаются в родителя, при `false` родителю восстанавливаются прежние |

- **Граничные события** поддерживаются только прерывающие и только у `task`, `scriptTask` и `serviceTask`. На остальное генератор падает с понятной ошибкой: `cancelActivity="false"`, `timeDate`/`timeCycle`, message и signal, событие у подпроцесса или callActivity, `errorRef` на несуществующую ошибку, длительность не в ISO (`2s`).
- **Не считаются методами:** `userTask` (ждёт пользователя, см. «Задачи пользователей»), `manualTask`, `sendTask`, `receiveTask`.
- **Назначение `userTask`** (`codegen/user_tasks.go`): атрибуты Camunda 7 `camunda:assignee`, `camunda:candidateUsers`, `camunda:candidateGroups`, `camunda:formKey`; если их нет — Zeebe `zeebe:assignmentDefinition` и `zeebe:formDefinition` (`formKey` или `formId`). Списки — через запятую. Переменная — `${path}`, `#{path}` или `=path` (путь через точку), в контракте всегда `${path}`; другие выражения (`${a + b}`, FEEL-список `=["a"]`) — ошибка генерации. Переменные назначения не обязаны быть полями `DataModel`: движок читает их из переменных процесса.
- **Сигнатура метода task:** `func X(ctx context.Context, svc *services.Services, data *model.DataModel) error`.
- **Сигнатура обработчика jobType:** `func X(ctx context.Context, svc *services.Services, job bpm.Job, data *model.DataModel) error`.
  - Метод меняет `data`. Переменных не из модели метод не видит, но они сохраняются.
  - Файлы со старой сигнатурой генератор не переписывает, на них падает проверка сборки. Чистую заглушку можно удалить, генератор создаст новую.
  - Методы выполняются синхронно внутри шага `moveNext`. Обработчики — асинхронно, отдельной задачей очереди, с повторами до `Retries`.
  - Обработчик должен быть идемпотентным; ключ — `job.InstanceUid + job.ElementId`.
  - Возвращённые переменные, как и у методов, идут в процесс дальше.
- **Условия шлюзов** (`conditions.go`): `func gateway1nptppd(data *model.DataModel) string`, во `flow-gen.go` — `bpm.Condition(gateway1nptppd)`. Переменные — поля модели: `amount == 55` → `data.Amount == 55`, тип берёт поле. FEEL-операторы (`and`, `or`, `not()`, одиночное `=`) переводятся в Go. Встроенные функции Go (`len(name)`) остаются вызовами. Исходное условие пишется комментарием над `if`. Несовпадение типов (поле `int`, а в условии `1.5`) ловит проверка сборки.
- **scriptTask** (`methods.go`, `scriptCode`):
  - с `resultVariable` скрипт — выражение: переменные строго переводятся в поля, результат присваивается полю `resultVariable` (может быть путём): `data.Greeting = data.Name + "!"`;
  - без `resultVariable` скрипт — код Go: в поля переводятся только имена, которые есть в `DataModel` (и пути от них), пакеты и локальные переменные остаются как есть. Неизвестное имя покажет проверка сборки, а не подсказка.
- **Тип по месту использования** (`expression_kinds.go`, `kindInferer`) выводится только для подсказки о недостающем поле: `a == 55` — int, `amount >= 1.5` — float64, `s == "x"` — string, `approved && ...` — bool.

### Решения DMN

Таблица решения превращается в функцию с цепочкой условий сразу в Go при генерации.

- **Где лежат.** `*.dmn` читаются из `process/` вместе с BPMN (`parser.ParseDmn`, модели в `internal/generator/models/dmn`). Id решения уникален во всех файлах (`CollectDecisions`). DMN-файлы вшиваются в `process/bpmn-gen.go` (map `DMN`), `bpm-gen.go` даёт `Decisions` — XML DMN по id решения для страницы истории.
- **Что генерируется** (`codegen/decisions.go`, шаблоны `templates/decision.go`):
  - `decision_<id решения>.go` в пакете каждого процесса, который вызывает решение: `func decide<Id>(data *model.DataModel) (<тип результата>, []string)` — результат и id сработавших правил в порядке таблицы;
  - `method_<id задачи>.go` у `businessRuleTask`: вызывает решение, передаёт `bpm.DecisionResult` в `bpm.RecordDecision(ctx, ...)` и пишет результат в поле модели `resultVariable`. Рукописный файл с этим именем — ошибка генерации, как у scriptTask.
- **Входы** — поля модели (`a` → `data.A`, `client.age`, имя с пробелами `customer category` — поле с таким тегом `json`), вычисляются один раз; локальная переменная называется по последнему сегменту, иначе `inputN`. Тип для ячеек — `typeRef` (`string`, `number`/`double` → float64, `integer`/`long` → int, `boolean`), без него — тип поля, без него — по литералам в ячейках. Вход-выражение (`age + 1`) переводится как условие. Вход, который не проверяет ни одно правило, не объявляется, и его поле не требуется.
- **Ячейки входов (unary tests):** пусто или `-`; значение (`"foo"`, `5`, `true`, переменная — поле модели) — равенство; `< <= > >=`; интервалы `[1..10]`, `]1..10[`, `(1..10]`; список через запятую — любой из них; `not(...)`; выражение с `?` (`? > a + 1`).
- **`null`** — только у входа-поля, которое может быть nil: указатель, map, срез, any. Проверка — `data.Limit == nil`; у указателя на простой тип правила сравнивают значение `bpm.Deref(data.Limit)`. У поля-значения `null` — ошибка генерации: отсутствующая переменная там просто нулевое значение.
- **Выходы** типизированы: тип — `typeRef`, без него — по литералам; пустая ячейка — нулевое значение типа. Несколько выходов — структура `decide<Id>Result` с полем на выход (имя — `name` выхода в PascalCase, тег `json` — `name`).
- **Hit policy и результат:**
  - `UNIQUE` (по умолчанию), `FIRST`, `ANY` — выход(ы) первого подошедшего правила. **Пересечение правил у UNIQUE и ANY не проверяется** (в `businessRule.dmn` правила пересекаются, Camunda на таком UNIQUE упала бы). Правила после безусловного не генерируются;
  - `RULE ORDER`, `COLLECT` — срез выходов подошедших правил, пустой, если ни одно;
  - `COLLECT` `SUM`/`MIN`/`MAX` (выход-число) и `COUNT` (int);
  - `PRIORITY`, `OUTPUT ORDER` — ошибка генерации.
- **Запись в `resultVariable`** (`resultAssign`):
  - один выход — значение в поле (в поле-указатель — адрес), список — срез (`[]string`);
  - несколько выходов — поле должно быть структурой модели с полями под имена выходов (указатель на структуру создаётся, если nil): `data.Discount.Value = result.Discount`;
  - список с несколькими выходами — поле-срез структур модели: `data.Lines = append(data.Lines, model.Line{...})`;
  - для первого подошедшего правила и агрегаций SUM/MIN/MAX поле не меняется, если ни одно правило не подошло, а в историю пишется `Result: nil`. Список и COUNT пишутся всегда.
- **Не поддерживается (ошибка генерации):** решение выражением (literal expression), зависимость от другого решения (`requiredDecision`), `businessRuleTask` с job worker (`zeebe:taskDefinition`), `businessRuleTask` без `decisionId` или `resultVariable`. FEEL-функции (даты, строки, `contains` и т.п.) не переводятся.
- Демо: `examples/demo/process/businessRule.dmn` (`Decision_1`: `a`, `b` → `c`; `SubDecision`: `a`, `c` → `d`) и `testBusinessRule.bpmn` (процесс `TestDecision`, две задачи бизнес-правил подряд).

### Контракт `bpm`

- `ActivateType`: `Flow`, `Queue`, `SyncQueue`, `StartSubprocess`, `StartNewProcess` (запустить и не ждать, генератор его не выдаёт), `End`, `Method`, `CallActivity`, `Timer` (промежуточный таймер), `Boundary` (граничное событие);
- `Expression.PropagateChildVariables` — для `CallActivity`; `Expression.Timer` — для `Timer`; `Expression.Boundaries []Boundary` — граничные события задачи, хелперы `Timers()` и `ErrorBoundary(code)`;
- `Boundary{ElementId, Type (BoundaryTypeTimer/BoundaryTypeError), Timer, ErrorCode}`;
- `DecisionResult{DecisionId, RuleIds, Result}` — вызов решения DMN; `RecordDecision(ctx, result)` передаёт его в `DecisionRecorder`, который движок кладёт в ctx метода (`WithDecisionRecorder`), без recorder ничего не делает;
- `Error{Code, Message}`, `NewError` — бизнес-ошибка для граничного события ошибки; константы переменных `ErrorCodeVariable` (`errorCode`) и `ErrorMessageVariable` (`errorMessage`);
- `CompleteType`: `Straight`, `Script`; у обоих типов есть `String()`;
- `Expression`, `DeploymentProcess`, `Process{Flows, BPMN}`, `Queue{TypeID, Retries, Handler}`;
- `ConditionFunc func(map[string]any) (string, error)`, `MethodFunc func(ctx, map[string]any) (map[string]any, error)`, `HandlerFunc func(ctx, bpm.Job, map[string]any) (map[string]any, error)` — переменные процесса map'ой. `Job`: `InstanceUid`, `ProcessName`, `BusinessKey`, `ElementId`, `JobType`, `Headers`, `LastAttempt`.
- Адаптеры на модели данных `T` (`data.go`): `Condition[T](func(*T) string)`, `Method[S, T](svc S, func(ctx, S, *T) error)`, `Handler[S, T](svc S, func(ctx, S, Job, *T) error)`. Сервисы `S` замыкаются при старте. Переменные → `T` через JSON (поле по тегу `json`), после метода поля `T` пишутся поверх переменных; переменные, которых нет в `T` (например, `errorCode`), сохраняются. Ошибка перевода (строка в поле `int`) — ошибка метода или условия, движок повторяет шаг.
- `ActivateTypeUserTask`, `Expression.UserTask *UserTask{Name, Assignee, CandidateUsers, CandidateGroups, FormKey}`; значение `${path}` — переменная процесса, остальные — литералы.
- `Deref[T](*T) T` — значение по указателю или нулевое; им решение DMN читает вход-указатель, у которого проверяется `null`.
- **Совместимость.** Шаблоны генератора и контракт в одном модуле, поэтому меняются в одном коммите. Но сгенерированный код живёт в сервисах-потребителях: несовместимое изменение типов `bpm` ломает их до перегенерации — после v1 это `/v2`.

## Очередь (`queue`)

Своя очередь вместо внешней библиотеки. Движку от неё нужно: виды задач с обработчиками, повторы и таймаут попытки, отложенная постановка (таймеры), ключ идемпотентности, отмена по ключу в tx и колбэки `OnSucceeded`, которые выполняются в той же tx, что завершает задачу.

- **Таблица `bpm_queue`:** `kind text`, `key text` (уникален в паре с `kind`, пока строка не удалена очисткой), `payload jsonb` (в движке это JSON сообщения — смотреть удобно прямо в базе), `status` (`pending`, `done`, `failed`, `cancelled`), `attempt`, `run_at`, `last_error`.
- **Аренда.** Выдача — `update ... from (select ... for update skip locked)`: `attempt + 1`, `run_at = now() + AttemptTimeout + 10s`. Задачу упавшего воркера по истечении аренды заберёт другой. Если аренду теряли столько раз, что попытки кончились, задача падает (`failed`) без запуска обработчика.
- **Завершение** (`complete`): в одной tx `update ... set status = 'done' where id = $1 and attempt = $2 and status = 'pending'`, затем колбэки. Первый update блокирует строку, поэтому пока идут колбэки, задачу не возьмёт другой воркер. 0 обновлённых строк — попытка больше не держит задачу (аренду забрали или задачу отменили): tx откатывается, колбэки не выполняются, результат отбрасывается.
- **Ошибки обработчика:** обычная — повтор с экспоненциальной задержкой и equal jitter (`RetryDelay`, удвоение, не больше `MaxRetryDelay`); `queue.ErrNonRetryable` или последняя попытка — `failed`; `queue.ErrCancel` — `cancelled`; паника — ошибка. Упавший колбэк откатывает завершение — задача повторяется.
- **`KindOptions.OnFailed(ctx, tx, task)`** выполняется в tx, которая помечает задачу `failed`, в том числе когда попытки кончились из-за потерянной аренды (обработчик тогда не запускается, поэтому колбэк вида, а не задачи). Упавший `OnFailed` откатывается, и задача помечается `failed` без него — иначе она падала бы по кругу. На `cancelled` не вызывается.
- **Пробуждение.** `PushTx` без задержки делает `pg_notify('bpm_queue', kind)` — уведомление уходит при коммите. Слушатель (`LISTEN` на отдельном соединении, забранном из пула через `Hijack`) будит воркеры вида. Отложенные задачи и повторы берёт опрос раз в `PollInterval` (1s ±25%). После (пере)подключения слушатель один раз будит все виды: уведомления до `LISTEN` теряются.
- **Остановка.** `Stop` перестаёт выдавать задачи и ждёт, пока воркеры доделают взятые (обработчики получают ctx без отмены, ограничены `AttemptTimeout`).
- **Очистка:** `done` и `cancelled` старше `Retention` (по умолчанию 7 дней) удаляются пачками по 1000; `failed` хранятся всегда — для разбора.
- **Ключ идемпотентности** уникален, пока строка не удалена: задача с тем же `(kind, key)` молча не ставится (`on conflict do nothing`). Поэтому ключи задач движка уникальны на каждую активацию (см. таймеры).

## Движок (`engine`)

- **Вход:** `engine.New(queue, engine.Config{...}, bpmgen.Processes(svc), bpmgen.Queues(svc))` до `queue.Start`. `StartProcess(ctx, name, variables map[string]any, opts ...StartOption) (uuid.UUID, error)`, опция `engine.WithBusinessKey(key)`; неизвестный процесс — `engine.ErrProcessNotFound`.
  - `StartProcessTx(ctx, tx, ...)` — старт в транзакции сервиса (процесс атомарно с его данными); `StartProcess` — он же в `Queue.InTx` (новый метод очереди, транзакция на её пуле, заменил `Push` в интерфейсе `engine.Queue`). В той же tx пишется строка экземпляра (`startInstance`), поэтому экземпляр виден со статусом `running` сразу. Раньше строку писал только шаг старта, и экземпляр с упавшим первым шагом не был виден, пока не кончатся попытки (около шести минут). Шаг старта вставляет строку ещё раз — `on conflict do nothing`. Id экземпляров — UUID v7. Опции пишут в экспортированную `StartOptions` (`ApplyStartOptions`), чтобы их мог разобрать фейк движка в тестах `httpapi`.
  - Flow всех процессов сливаются в одну map; дубликат id flow — ошибка.
  - Очереди уже объединены генератором; повтор `TypeID` в `Queues` — ошибка (`sortedQueues`).
- **Виды задач** — строки: системная очередь `<KindPrefix>.system` (старты, шаги, таймеры), service task — `<KindPrefix>.task.<jobType>`. `KindPrefix` по умолчанию `bpm`; разные префиксы разводят по одной базе экземпляры с разным кодом процессов (раньше для этого собирали второй бинарник с другим `int16`-kind через `-overlay`).
- **`Config`:** `SystemWorkers` (4), `SystemMaxAttempts` (10 — упавший метод откатывает шаг, и очередь его повторяет), `AttemptTimeout` (30s), `RetryDelay` (1s), `TaskWorkers` (1 на jobType), `History`, `RulesHistory`, `Logger`.
- **Service task** (`bpm_task_handler.go`, `runHandler` в `generated_funcs.go`): задача своего вида вызывает обработчик jobType. Ошибка обработчика — повтор этой же задачи; при успехе в `OnSucceeded` выполняется `moveNext(Complete)`.
- **`moveNext`** (`move_next.go`) — машина состояний. Все побочные эффекты пишутся в tx завершения текущей задачи очереди, поэтому шаг применяется атомарно.
  - `Method`: вызвать метод, затем `completeElement`. Ошибка метода откатывает весь шаг, и очередь повторяет **предыдущую** задачу, например service task перед методом.
  - `Script`: `Condition` возвращает id flow. Если функции нет или ни одна ветка не подошла и нет default flow — ошибка.
  - `StartSubprocess` и `CallActivity` (`startChildProcess`): токен переходит в новый экземпляр, связь с родителем хранится в `instanceMessage.ParentProcess`. End дочернего процесса возвращает токен в элемент родителя через `Complete`. Для `CallActivity` без проброса переменных в `ParentProcess` сохраняются переменные родителя (`RestoreVariables`).
  - `SyncQueue`: слияние параллельных веток через таблицу `bpm_parallel_sync` (`parallel_sync.go`). Переменные веток объединяются.
- **Таймеры и граничные события** (`timers.go`).
  - Таймер — системная задача `_system.timer`, отложенная `queue.WithDelay`. Воркер не ждёт, таймер переживает рестарт. В payload: `boundary` (пусто у промежуточного таймера) и `delay` для отладки.
  - Промежуточный таймер (`Timer`): при активации ставит задачу и пишет `activated`, по срабатыванию — `moveNext(Complete)`.
  - Service task с граничными таймерами гоняется со своими таймерами. Отменить задачу, которую воркер уже выполняет, нельзя, поэтому победителя выбирает строка `bpm_wait`: её вставляет активация (`startWait`, id в `instanceMessage.WaitId`), удаляет первый завершившийся (`finishWait`). Проигравший видит 0 удалённых строк и ничего не делает. Победитель отменяет `CancelByKeyTx` задачу (ключ — wait_id) и таймеры (ключ `<wait_id>/<событие>`).
  - Ключи уникальны на каждую активацию: очередь молча пропускает задачу с ключом, который ещё хранится, а задачу в цикле активируют повторно.
  - Отмена задачи, которую воркер уже выполняет, помечает её `cancelled`, и её результат отбрасывается при завершении (лишняя страховка). Корректность держится на `bpm_wait`, не на отмене.
  - `task`/`scriptTask` синхронные: граничный таймер — `context.WithTimeout` на метод (`activateMethod`). Срабатывает, только если метод слушает `ctx`; результат метода после дедлайна отбрасывается.
  - Срабатывание события (`leaveByBoundary`): `terminated` у задачи, `completed` у события, токен по исходящим flow события.
- **Ошибки задач.** Бизнес-ошибка — `bpm.NewError(code, message)`. Её ловит граничное событие с этим кодом, иначе событие без кода. Код и текст кладутся в переменные `errorCode` и `errorMessage`.
  - Service task (`taskErrorStep`): пойманная ошибка завершает задачу и ведёт токен по событию. Непойманная `bpm.Error` — `queue.ErrNonRetryable`, без повторов. Техническая ошибка повторяется, а после последней попытки её ловит событие без кода.
  - Метод: пойманная `bpm.Error` — переход по событию в том же шаге; техническая ошибка и непойманная `bpm.Error` откатывают шаг, и очередь его повторяет.
- **Задержка таймеров** — до `PollInterval` (1s ±25%): отложенные задачи берёт опрос. Синхронные методы выполняются в шагах системной очереди, поэтому долгий метод занимает один из `SystemWorkers`. Шаги без задержки приходят по NOTIFY почти сразу.
- **История** (`history.go`). `logHistory` пишет событие `completed` после выполнения каждого элемента (в начале `completeElement` и на End event), `activated` при постановке задачи service task в очередь и промежуточного таймера, `terminated` у задачи, которую прервало граничное событие. Запись содержит событие, процесс, экземпляр, `parent_instance_uid` и `parent_element_id` (экземпляр и элемент родителя для подпроцесса и callActivity, у корневого NULL), элемент, ActivateType, переменные и время.
  - `HistoryConfig{ToLog, ToDB func() bool}` — функции, читаются на каждый элемент, поэтому сервис может менять их на лету. В демо — флаги `-history-log`, `-history-db`.
  - На экран история выводится строкой в stdout. В базу `writeHistoryDB` пишет в `bpm_history` в tx шага, поэтому история атомарна с переходом токена.
  - Переменные пишутся только для старта процесса (StartElement любого flow, в том числе встроенного подпроцесса и процесса callActivity) и задач: `Method`, `Queue`, `CallActivity`, а также граничных событий — там видны `errorCode` и `errorMessage` (`recordsVariables`). У остальных записей `variables` NULL, чтобы история не росла снимком на каждый sequence flow.
  - Вывод на экран при откате шага не отменяется, поэтому при повторе задачи строки могут задвоиться.
- **Экземпляры** (`instances.go`, таблица `bpm_process_instance`): id, тип (`process_name` — id процесса в BPMN, у встроенного подпроцесса `<процесс>.<подпроцесс>`), `business_key`, `parent_instance_uid`, `status`, `created_at`, `updated_at`.
  - Пишутся вместе с историей, только при `History.ToDB`, в tx шага. Корневой экземпляр — в tx `StartProcess`/`StartProcessTx` вместе с задачей старта (и ещё раз, без эффекта, в `startRoot`); встроенный подпроцесс и callActivity — когда токен входит в них (`startChildProcess`), `StartNewProcess`. У экземпляров, начатых при выключенном `ToDB`, строки нет.
  - Статусы: `running` → `completed` на End event экземпляра; `failed` — по `OnFailed` очереди: упал шаг системной очереди или service task. Падает экземпляр задачи и все его предки по `ParentProcess` (они ждут End, которого не будет); строка вставляется, если её нет (упал сам старт). `failed` не перезаписывается в `completed`: токенов движок не считает, поэтому до End могла дойти другая параллельная ветка — по той же причине несведённые параллельные ветки до разных End дают `completed` на первом.
  - `updated_at` обновляется с каждой записью истории (`touchInstanceQuery` в `writeHistoryDB`) и при смене статуса.
  - Бизнес-ключ — свободный текст из старта, наследуется дочерними экземплярами (в `instanceMessage.BusinessKey`) и передаётся обработчикам в `bpm.Job`. Индекса по нему нет.
  - Индексы `(status, process_name, instance_uid)` и `(process_name, instance_uid)`: поиск по статусу, по статусу и типу, по типу. Выборка — от новых к старым по `instance_uid` (UUID v7 растёт со временем), страница — `Before`.
  - Чтение: `HistoryReader.GetInstance(id)` (нет — `ErrInstanceNotFound`), `ListInstances(InstanceFilter{ProcessName, Status, BusinessKey, Before, Limit})` (по умолчанию 100, не больше 1000; неизвестный статус — `ErrInvalidStatus`). Условия собираются из заданных полей, а не `$1 = '' or ...`, чтобы планировщик брал индексы.
- **Задачи пользователей** (`user_tasks.go`, таблица `bpm_user_task`), как в Camunda 7:
  - Токен приходит в `userTask` → `activateUserTask` подставляет переменные назначения (строка или список строк), вставляет строку с токеном (`message`) и пишет `activated`.
  - **Назначение, которое не вычислилось** (переменной нет, не строка, у исполнителя несколько значений), пропускается с предупреждением `user task assignment skipped` в логе. Раньше это была ошибка шага, и экземпляр застревал навсегда: шаг повторялся с той же ошибкой до `failed`, а `Complete` прошлой задачи уже ответил успехом, и её исполнитель ошибки не видел (так было с демо, запущенным без `initiator`). Теперь задача без исполнителя попадает к кандидатам, а без кандидатов — в очередь всех. Таблица пишется всегда, не зависит от `History.ToDB`: это состояние процесса, а не история.
  - `engine.NewUserTaskService(pool, engine)`: `ListTasks(TaskFilter)`, `GetTask(id)` (с переменными процесса на момент создания — данные для формы), `Claim(id, user, groups)`, `Unclaim(id, user)`, `Complete(id, user, variables)`. Каждое действие — tx с `select ... for update` строки задачи.
  - **Очередь:** задача с `assignee` — личная; без него её видят и могут взять кандидаты: пользователь из `candidate_users` или участник группы из `candidate_groups`. Задачу без кандидатов и исполнителя может взять любой. Группы пользователя передаёт вызывающий (в движке нет пользователей и авторизации). В `TaskFilter` `Assignee` и `CandidateUser`/`CandidateGroups` объединяются через «или» — «мои и доступные мне», кандидаты — только среди задач без исполнителя. Задачи без исполнителя и кандидатов входят в очередь любого кандидата: их может взять любой.
  - **Правила:** `Claim` чужой взятой задачи — `ErrTaskClaimed`, не кандидат — `ErrNotCandidate`, повторный `Claim` своей — ничего; `Unclaim` и `Complete` — только исполнитель (`ErrNotAssignee`), поэтому задачу кандидатов сначала берут в работу. Строже, чем `TaskService.complete` Camunda 7, который исполнителя не проверяет. Завершённая — `ErrTaskCompleted`.
  - **Завершение:** в одной tx задача помечается `completed` (`completed_by`, `completed_at`), переменные формы добавляются к переменным токена, и ставится системная задача `_system.userTask` с ключом task_uid. Токен дальше ведёт очередь: ошибка следующих шагов (упавший метод) повторяется очередью, а не возвращается в API. Завершённые задачи не удаляются.
  - Статус в фильтре — литерал в SQL, а не параметр: частичные индексы открытых задач (`where status = 'created'`) не подходят к общему плану prepared statement. Индексы: `assignee`, GIN по `candidate_users` и `candidate_groups`, `process_name` — по открытым; `instance_uid` — по всем.
- **История бизнес-правил** (`rules_history.go`): `activateMethod` кладёт в ctx метода `decisionRecorder`, который копит вызовы `bpm.RecordDecision`. Пишутся они (`logDecisions`) только после успешного метода, в tx шага: если метод упал или его прервал граничный таймер, правил в истории нет.
  - На экран: строка `rules history: process=... instance=... element=... decision=... rules=[...] result=...`.
  - В базу: `bpm_rules_history` (`rule_ids jsonb` — массив id правил, `result jsonb`; индекс `(instance_uid, element_id, id)`).
- **Чтение истории:** `engine.NewHistoryReader(pool, processes, bpmgen.Decisions)` — `GetHistory(id, withChildren)`, `GetDiagram(id)`, `GetRulesHistory(id, elementId)`, `GetInstance(id)`, `ListInstances(filter)`. Читает через интерфейс `Querier` (`*pgxpool.Pool` подходит).

## Схема БД (`migrations`)

- Начальная миграция `0001_init.sql`: `bpm_queue`, `bpm_parallel_sync`, `bpm_wait`, `bpm_history`, `bpm_rules_history`; `0002_process_instances.sql` — `bpm_process_instance`; `0003_user_tasks.sql` — `bpm_user_task`; `0004_user_tasks_open_to_all.sql` — частичный индекс открытых задач без кандидатов. Девять goose-миграций pg-bpm схлопнуты: у нового продукта нет баз, которые нужно мигрировать. `rules_history` переименована в `bpm_rules_history`, префикс `public.` убран из запросов — работает `search_path`.
- `migrations.Up(ctx, pool)` применяет недостающие миграции под `pg_advisory_lock` (поды стартуют одновременно), каждую в своей tx, версии — в `bpm_schema_migrations`. Выпущенный файл не меняют: изменение схемы — новый `NNNN_<имя>.sql`.
- Сервис, у которого свой инструмент миграций, может взять SQL через `migrations.List()` и не вызывать `Up`.

## API, консоль и страница истории

- **`httpapi.Register(mux, engine, historyReader)`:**
  - `GET /api/v1/processes` → `{"processes": [{"id", "name", "instances": {"running": n, ...}}]}` — корневые процессы движка (`Engine.Processes()`, название из атрибута `name` процесса в BPMN, разбирается в `engine.New`) с числом экземпляров по статусам (`HistoryReader.CountInstances`, `group by` по `bpm_process_instance`);
  - `GET /api/v1/processes/{name}` → процесс с `bpmn`; нет — 404;
  - `POST /api/v1/processes/{name}/instances` с телом `{"variables": {...}, "businessKey": "..."}` (ключ необязателен) → `201 {"id": "..."}`; неизвестный процесс — 404. Переменные — любые JSON-значения (в gRPC были только строки).
  - `GET /api/v1/instances?processName=&status=&businessKey=&limit=&before=` → `{"instances": [...]}` (пустой список — `[]`); неверные `status`, `limit`, `before` — 400.
  - `GET /api/v1/instances/{id}` → экземпляр; нет строки — 404.
- **`httpapi.RegisterUserTasks(mux, engine.NewUserTaskService(pool, engine))`** — отдельно от `Register`, сервису без задач пользователей не нужен:
  - `GET /api/v1/tasks?assignee=&candidateUser=&candidateGroup=&processName=&instanceId=&businessKey=&status=&limit=&before=` → `{"tasks": [...]}`; `candidateGroup` можно повторять или писать через запятую, `status` — `created` (по умолчанию) или `completed`;
  - `GET /api/v1/tasks/{id}` → задача с `variables`;
  - `POST /api/v1/tasks/{id}/claim` `{"userId", "groups"}`, `/unclaim` `{"userId"}`, `/complete` `{"userId", "variables"}` → 204;
  - ошибки: 400 — неверный запрос или нет `userId`, 403 — не кандидат, 404 — нет задачи, 409 — завершена, взята другим или не назначена пользователю.
  - `GET /api/v1/instances/{id}/history?withChildren=true` → `{"records": [...]}`; нет записей — 404.
- **История экземпляра** — `/ui/history/?instanceId=<id>` (`webui/static/history.*`, обработчики в `webui/history.go`). Раньше жила в пакете `historyview` на `/history/`; пакет влит в `webui`, чтобы история была частью консоли с той же шапкой. Старые адреса `/history/...` перенаправляются (301) на `/ui/history/` с теми же параметрами.
  - Шапка — как у консоли, подсвечена вкладка «Экземпляры». Поиска по id нет: экземпляр приходит в адресе из списка «Экземпляры», ссылок консоли и переходов в подпроцесс; без `instanceId` страница ссылается на список. Страница подключает общий `style.css` консоли, затем `history.css` — раскладку на всю высоту, диаграмму, панель и окно правил, и переопределения общих правил, которые ей мешают (`main` консоли ограничен по ширине, а здесь `main` — холст диаграммы).
  - bpmn-js 18.31.0 и dmn-js 17.12.0 вшиты через `go:embed` в `webui/static/vendor/<библиотека>-<версия>` (с LICENSE), ими пользуются и история, и схемы процессов консоли. Логотип bpmn.io по лицензии не скрывать. Файлы `vendor/` кэшируются надолго, страницы и их скрипты — `no-cache`.
  - `GET /ui/history/api/diagram?instanceId=` → `engine.Diagram`: BPMN процесса и записи экземпляра с его встроенными подпроцессами. Экземпляры callActivity нарисованы в своих BPMN и не входят.
  - Маркер элемента — по его последнему событию: `completed` закрашивается, `activated` обводится пунктиром, `terminated` — красной рамкой.
  - Клик по элементу открывает боковую панель: проходы элемента с переменными и вызванные экземпляры callActivity. Выбранный элемент сохраняется в адресе как `&element=<id>`. Двойной клик по callActivity с одним вызванным экземпляром открывает его, у дочернего экземпляра сверху ссылка на родителя; переходы через `history.pushState`.
  - У User Task блок «Назначение» (`GET /ui/history/api/tasks?instanceId=&elementId=` → `HistoryReader.GetUserTasks`): задача на каждый приход токена — статус (ждёт исполнителя, в работе, выполнена), когда создана, исполнитель и время назначения (`claimed_at`; у задачи, назначенной при создании, — время создания), кто может взять (кандидаты; без них — любой пользователь или только исполнитель), кто и когда выполнил, форма. Как и правила, задачи берутся по всем экземплярам элемента на диаграмме. Истории взятий и возвратов нет: после `unclaim` прежний исполнитель не виден.
  - У Business Rule Task кнопка «Показать сработавшие правила» (`GET /ui/history/api/rules?instanceId=&elementId=`): окно с таблицей решения, строки сработавших правил подсвечиваются по `data-row-id`; несколько вызовов переключаются кнопками.
- **`webui.Register(mux, historyReader)`** — консоль `/ui/` и история `/ui/history/` (статика через `go:embed`, чистый JS без сборки). Консоль работает поверх `httpapi` (`Register` и `RegisterUserTasks`), поэтому они на том же mux. Ссылки меню абсолютные (`/ui/#/processes`), одинаковые на обеих страницах. В демо `/` ведёт на `/ui/`.
  - Вкладка и фильтры — в адресе после `#`: `#/processes`, `#/start?process=X`, `#/instances?process=X&status=failed&key=K`, `#/tasks?user=bob&groups=managers,admins`. Ссылку на отфильтрованный список можно передать, «назад» в браузере работает.
  - **Процессы:** список с числом выполняющихся, завершённых и упавших экземпляров (число — ссылка на отфильтрованный список), схема процесса, переход к запуску.
  - **Запуск:** процесс, бизнес-ключ, переменные строками «имя, тип (строка, число, да/нет, JSON), значение» или одним JSON-объектом.
  - **Экземпляры:** фильтры процесса, статуса и бизнес-ключа, страницы по 20 через курсор `before` (запрос `limit = 21`: лишний говорит, что есть следующая страница; курсоры прежних страниц хранятся для «Новее»). Строка ведёт в историю. Id показывается хвостом: начало UUID v7 — время, у запущенных подряд оно совпадает.
  - **Задачи:** пользователь и группы — текстом (последние запоминаются в `localStorage`), «Мои задачи» (`assignee`) и «Можно взять» (кандидаты), кнопки взять, вернуть, выполнить. Очередь показывается и по одним группам, без пользователя: тогда нет «Моих задач», а «Взять» выключена — взять задачу может только пользователь. Выполнение — окно с переменными процесса и редактором результата; через секунду список обновляется ещё раз — следующую задачу создаёт очередь.
  - Проверялась headless Chrome через chromedp (скрипт вне репозитория): все вкладки, схема, запуск, взять и выполнить задачу, страницы списка, переход в историю, ширина телефона.
- Раньше API и страница жили на двух портах (public 7000 и admin 7001); теперь всё на одном `http.ServeMux`, порт выбирает сервис (демо — `:8080`).

## Документация

Пользовательская документация — `docs/*.md` (оглавление `docs/README.md`), этот файл — журнал решений для разработчиков.

- Картинки — `docs/images/`. Схемы — SVG, экспортированные bpmn-js (`viewer.saveSVG()`, тот же вшитый bpmn-js 18.31.0) с белым фоном, вставленным после открывающего `<svg>`: без него в тёмной теме GitHub линии не видны. `demo-*.svg` — из `examples/demo/process/*.bpmn`, остальные — из `docs/images/src/*.bpmn` (маленькие процессы с DI, открываются в Camunda Modeler).
- Скриншоты консоли и истории (`console-*.png`, `history-*.png`) сняты headless Chrome (chromedp, скрипт вне репозитория) с демо на чистой базе; таймеры и ошибки — с копии `TestTimerAndException`, где заглушки дописаны, как в «Эксперименты» `docs/examples.md`. После изменения консоли или процессов демо картинки нужно переснять.
- Команды и выводы в документации проверены на демо и на сервисе, собранном с нуля по `docs/quickstart.md`.

## Тесты

- `make test` — всё, включая интеграционные тесты на Postgres из `GO_BPM_TEST_DSN` (база очищается!). Пакеты гоняются по одному (`-p 1`): тесты с базой делят таблицы. `make test-unit` — без базы, интеграционные пропускаются.
- `engine`: фейковая очередь и фейковые tx (`process_test.go`, `timers_test.go`, `decision_test.go`, `history_test.go`, `call_activity_test.go`, `instances_test.go`, `user_tasks_test.go`); `TestEndToEnd` (`e2e_test.go`) — все демо-процессы на настоящей очереди и базе до End event, с чтением истории, диаграмм, правил и экземпляров (статус `completed`, бизнес-ключ у дочерних). `TestUserTask` в нём пропускается: его проходит `TestUserTasks` (`user_tasks_e2e_test.go`) — очередь группы, claim/unclaim/complete и ошибки, исправление инициатором, End.
  - `timers_test.go`: таймеры без ожидания — тест сам достаёт задачу таймера из фейковой очереди и вызывает `fireTimer`; `fakeWaitTx` эмулирует `bpm_wait`.
  - Логика обработчиков тестируется через `runHandler`, ошибки — через `taskErrorStep`, который возвращает шаг для `OnSucceeded`.
- `queue`: интеграционные — колбэк в tx завершения, `OnFailed`, NOTIFY, ключ идемпотентности, повторы, `ErrNonRetryable`/`ErrCancel`/паника, упавший колбэк, задержка, отмена во время выполнения.
- `httpapi`, `webui` — через `httptest` (у `webui` раздача страниц, API истории и перенаправления, JS тестами не покрыт); генератор — юнит-тесты `codegen` и `utils`.
- Демо-процесс `testTimerAndException.bpmn` управляется переменными: `sleep1` (секунд у «это 1», таймер 2s), `fail2`, `sleep4` (у service task «это 4», таймер 1s), `fail5`. Методы и обработчики демо — заглушки, а в `DataModel` этих полей нет: логику нужно вернуть вместе с полями.

## Окружение

- Локально: Postgres на `localhost:5432` (postgres/postgres), базы `go_bpm` (демо, `make run`) и `go_bpm_test` (тесты).
- Модули — с публичного `proxy.golang.org`. Если в окружении задан корпоративный `GOPROXY`, для этого репозитория: `GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org`.
- **IDE и диск.** Файлы, открытые в GoLand, могут сохраниться поверх изменений на диске. Перед коммитом проверять, что версия на диске — актуальная.

## Откуда пришли

pg-bpm — три отдельных модуля: `pg-bpm-lib` (→ `bpm`), `pb-bpm-gen` (→ `cmd/go-bpm-gen` + `internal/generator`), `pg-bpm` (→ `engine`, страница истории — теперь в `webui`, демо). Что заменено при переезде:

| Было | Стало |
|---|---|
| внешняя библиотека очереди, kind `int16` из хеша jobType | `queue`, виды-строки `<prefix>.task.<jobType>` |
| gRPC + grpc-gateway, admin-роутер chi | `httpapi` и `webui` на `net/http` |
| корпоративный логгер | `log/slog` |
| realtime config, Vault, k8s values | `HistoryConfig` — функции; в демо флаги и переменные окружения |
| корпоративная обёртка над Postgres и её tx | `pgxpool`, `queue.Tx` (подходит `pgx.Tx`) |
| goose-миграции | `migrations.Up` |
| три репозитория, теги, GOPRIVATE и PAT | один модуль на github |

## Открытые задачи

- Задачи пользователей, не сделано: граничные события у `userTask` (таймер эскалации), делегирование и смена исполнителя администратором, `dueDate`/`priority`, отмена открытых задач, когда экземпляр падает. Пользователь и его группы приходят в запросе — авторизацию делает сервис.
- Таймеры и ошибки, не сделано: непрерывающие события, `timeDate`/`timeCycle`, граничные события у подпроцесса и callActivity, всплытие непойманной `bpm.Error` к родителю.
- DMN, не сделано: зависимые решения (`requiredDecision`), literal expression, `PRIORITY`/`OUTPUT ORDER`, FEEL-функции, проверка пересечений у `UNIQUE`. В истории правил нет значений входов таблицы. Окно правил не открывается ссылкой (`?element=` открывает только панель).
- Реализовать рукописные методы `test_bpm_process` и обработчик `handlers/do_some_async.go` в демо (сейчас заглушки с TODO).
- Метрики очереди (прежняя очередь давала их из коробки): глубина по видам, упавшие задачи, время попытки. Сейчас видно только запросом к `bpm_queue`.
- Мелочь: имена файлов из id элементов не проверяются на суффиксы `_test`, `_linux`, `_amd64` и т.п. Go трактует такие файлы особо.
