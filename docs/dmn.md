# Решения DMN

Таблица решений DMN превращается при генерации в Go-функцию — цепочку `if` над полями модели данных. В рантайме
нет интерпретатора FEEL: правило, которое не компилируется, покажет генератор.

Что такое DMN и hit policy — в [Принципах работы](concepts.md#dmn-за-пять-минут). Здесь — как go-bpm читает
таблицы, что генерирует и что пишет в историю.

## Как подключить решение

1. Нарисуйте таблицу в Camunda Modeler (Create new → DMN diagram, Camunda 8) и сохраните `*.dmn` в `process/`.
2. В процессе добавьте **Business rule task**: Implementation — DMN decision, **Decision ID** — id решения (не
   файла), **Result variable** — куда положить результат.
3. Добавьте в модель данных поля входов и результата — генератор подскажет, какие.

```xml
<bpmn:businessRuleTask id="CalcOffer" name="Рассчитать скидку">
  <bpmn:extensionElements>
    <zeebe:calledDecision decisionId="Offer" resultVariable="offer" />
  </bpmn:extensionElements>
</bpmn:businessRuleTask>
```

- Id решения уникален во всех `*.dmn` в `process/`. В одном файле может быть несколько решений.
- Одно решение можно вызывать из нескольких задач и процессов.
- Для каждого процесса, который вызывает решение, в его пакет генерируется `decision_<id решения>.go`, а задаче —
  `method_<id задачи>.go`, который его вызывает.

## Пример: скидка и комиссия

Таблица `Offer`, hit policy **FIRST** — первое подошедшее правило:

| Правило | Сумма заказа (`amount`, number) | Категория (`category`, string) | `discount` (number) | `reason` (string) |
|---|---|---|---|---|
| VipBig | `>= 10000` | `"vip"` | `15` | `"vip, крупный заказ"` |
| Big | `>= 10000` | `-` | `10` | `"крупный заказ"` |
| Status | `[1000..10000[` | `"vip","gold"` | `5` | `"статус клиента"` |

Генерируется:

```go
// decideOfferResult - выходы решения Offer
type decideOfferResult struct {
	Discount float64 `json:"discount"`
	Reason   string  `json:"reason"`
}

// decideOffer - решение Offer «Скидка» из pricing.dmn, hit policy FIRST.
func decideOffer(data *model.DataModel) (decideOfferResult, []string) {
	amount := data.Amount     // Сумма заказа
	category := data.Category // Категория клиента

	// VipBig: >= 10000 | "vip" => 15 | "vip, крупный заказ"
	if amount >= 10000 && category == "vip" {
		return decideOfferResult{Discount: 15, Reason: "vip, крупный заказ"}, []string{"VipBig"}
	}
	// Big: >= 10000 | - => 10 | "крупный заказ"
	if amount >= 10000 {
		return decideOfferResult{Discount: 10, Reason: "крупный заказ"}, []string{"Big"}
	}
	// Status: [1000..10000[ | "vip","gold" => 5 | "статус клиента"
	if amount >= 1000 && amount < 10000 && (category == "vip" || category == "gold") {
		return decideOfferResult{Discount: 5, Reason: "статус клиента"}, []string{"Status"}
	}
	return decideOfferResult{}, nil
}
```

Два выхода — значит, `resultVariable` должна быть структурой модели с полями под имена выходов:

```go
type DataModel struct {
	Amount   float64 `json:"amount"`
	Category string  `json:"category"`
	Offer    Offer   `json:"offer"`
	// ...
}

type Offer struct {
	Discount float64 `json:"discount"`
	Reason   string  `json:"reason"`
}
```

Метод задачи генерируется полностью:

```go
func CalcOffer(ctx context.Context, svc *services.Services, data *model.DataModel) error {
	result, rules := decideOffer(data)
	var recorded any
	if len(rules) > 0 {
		recorded = result
	}
	if err := bpm.RecordDecision(ctx, bpm.DecisionResult{DecisionId: "Offer", RuleIds: rules, Result: recorded}); err != nil {
		return err
	}
	if len(rules) > 0 {
		data.Offer.Discount = result.Discount
		data.Offer.Reason = result.Reason
	}
	return nil
}
```

Таблица `Fee`, hit policy **COLLECT SUM** — сумма выходов всех подошедших правил:

| Правило | `amount` (number) | `urgent` (boolean) | `fee` (number) |
|---|---|---|---|
| Base | `> 0` | `-` | `100` |
| Large | `> 5000` | `-` | `200` |
| Urgent | `-` | `true` | `300` |

```go
func decideFee(data *model.DataModel) (float64, []string) {
	amount := data.Amount // Сумма
	urgent := data.Urgent // Срочно
	var values []float64
	var rules []string

	// Base: > 0 | - => 100
	if amount > 0 {
		rules = append(rules, "Base")
		values = append(values, 100)
	}
	// Large: > 5000 | - => 200
	if amount > 5000 {
		rules = append(rules, "Large")
		values = append(values, 200)
	}
	// Urgent: - | true => 300
	if urgent {
		rules = append(rules, "Urgent")
		values = append(values, 300)
	}
	if len(values) == 0 {
		return 0, nil
	}
	result := values[0]
	for _, value := range values[1:] {
		result += value
	}
	return result, rules
}
```

Заказ `{"amount": 12000, "category": "vip", "urgent": true}` даёт `offer = {"discount": 15, "reason": "vip, крупный
заказ"}` и `fee = 600`, а в истории правил `Fee` — `rule_ids: ["Base", "Large", "Urgent"]`, `result: 600`.

## Входы

Вход таблицы — колонка Input, её **Expression** — переменная процесса.

| Expression | Поле модели |
|---|---|
| `amount` | `data.Amount` (тег `json:"amount"`) |
| `client.age` | `data.Client.Age` |
| `customer category` | поле с тегом `json:"customer category"` |
| `age + 1` | выражение над полями, переводится как условие шлюза |

- Каждый вход вычисляется один раз, в локальную переменную с именем последнего сегмента пути (`age`), иначе
  `inputN`. Подпись колонки (label) — комментарий.
- **Тип** для сравнения в ячейках — `typeRef` входа: `string`, `number`/`double` → `float64`, `integer`/`long` →
  `int`, `boolean`. Без `typeRef` — тип поля модели, без поля — по литералам в ячейках.
- Вход, который не проверяет ни одно правило (все ячейки `-`), не объявляется, и поля для него не нужно.

## Ячейки входов

| Ячейка | Смысл | Go |
|---|---|---|
| пусто или `-` | любое значение | — |
| `"vip"`, `5`, `true` | равно | `category == "vip"` |
| `limit` | равно полю модели | `amount == data.Limit` |
| `< 5`, `<= 5`, `> 5`, `>= 5` | сравнение | `amount > 5` |
| `[1..10]` | от и до включительно | `x >= 1 && x <= 10` |
| `]1..10[` или `(1..10)` | без границ | `x > 1 && x < 10` |
| `[1..10[`, `(1..10]` и т.п. | одна граница включена | |
| `"vip","gold"` | любое из значений | `(c == "vip" \|\| c == "gold")` |
| `not("vip")`, `not(< 5)` | отрицание | `!(...)` |
| `? > a + 1` | выражение, `?` — значение входа | `amount > data.A+1` |
| `null` | значения нет | `data.Limit == nil` |

**`null`** допустим только у входа-поля, которое может быть `nil`: указатель, map, срез, `any`. У указателя на
простой тип остальные правила сравнивают значение (`bpm.Deref(data.Limit)`, nil → нулевое значение). У поля-значения
(`float64`, `string`) `null` — ошибка генерации: отсутствующая переменная там просто нулевое значение.

## Выходы

- Тип выхода — `typeRef`, без него — по литералам в ячейках. Пустая ячейка выхода — нулевое значение типа.
- **Один выход** — результат пишется в поле `resultVariable` (в поле-указатель — адрес значения).
- **Несколько выходов** — `resultVariable` должна быть структурой модели с полями под **имена** выходов (Output
  name, не label): выход `discount` → поле с тегом `json:"discount"`. Указатель на структуру создаётся, если он nil.
- **Список** (COLLECT, RULE ORDER) с одним выходом — поле-срез (`[]string`); с несколькими — срез структур модели:
  `data.Lines = append(data.Lines, model.Line{...})`.

## Hit policy

| Hit policy | Результат | Если ни одно правило не подошло |
|---|---|---|
| `UNIQUE` (по умолчанию) | выходы подошедшего правила | поле не меняется, в истории `result: null` |
| `FIRST` | выходы первого подошедшего правила | так же |
| `ANY` | выходы первого подошедшего правила | так же |
| `RULE ORDER` | срез выходов всех подошедших, в порядке таблицы | пустой срез |
| `COLLECT` | срез выходов всех подошедших | пустой срез |
| `COLLECT SUM`, `MIN`, `MAX` | сумма, минимум, максимум (выход — число) | поле не меняется |
| `COLLECT COUNT` | число подошедших правил (`int`) | `0` |
| `PRIORITY`, `OUTPUT ORDER` | ошибка генерации | |

> **Пересечение правил у `UNIQUE` и `ANY` не проверяется.** go-bpm возвращает первое подошедшее, как `FIRST`.
> Camunda на пересекающихся правилах `UNIQUE` упала бы. Если порядок важен, явно ставьте `FIRST`.

Правила после безусловного (все ячейки `-`) у политик «первое подошедшее» не генерируются — до них не дойти.

## История правил

Каждый вызов решения записывается, если включена история правил (`Config.RulesHistory`):

- в лог: `rules history: process="TestDecision" instance=... element="Activity_0xzljrt" decision="Decision_1"
  rules=["DecisionRule_04e4bk8"] result="correct foobar"`;
- в базу: таблица `bpm_rules_history` — `decision_id`, `rule_ids` (JSON-массив), `result` (JSON).

Записывается только успешный вызов, в транзакции шага: если метод задачи упал или его прервал граничный таймер,
правил в истории нет.

В консоли у business rule task в истории экземпляра есть кнопка **«Показать сработавшие правила»**: открывается
таблица решения, строки сработавших правил подсвечены. Если задача выполнялась несколько раз (цикл), вызовы
переключаются кнопками.

Значения входов в истории правил не хранятся — смотрите переменные задачи в истории элементов.

## Что не поддерживается

Ошибка генерации:

- решение-выражение (literal expression) вместо таблицы;
- зависимость от другого решения (`requiredDecision`, информационные связи в DRD) — вызывайте решения отдельными
  задачами подряд, как в демо `TestDecision`;
- hit policy `PRIORITY` и `OUTPUT ORDER`;
- business rule task с job worker (`zeebe:taskDefinition`) или без `decisionId`/`resultVariable`.

Не переводятся FEEL-функции (`date()`, `contains()`, `string length()` и т.п.), даты и длительности, контексты и
списки FEEL. Для таких правил подготовьте вход методом перед задачей: например, `isWorkingDay bool` вместо
`day of week(date)`.
