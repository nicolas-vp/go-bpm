# go-bpm

BPM-движок на PostgreSQL и генератор Go-кода из BPMN/DMN — один модуль `github.com/nicolas-vp/go-bpm`.

Перед работой прочитай @docs/context.md: раскладка пакетов, сгенерированного кода, семантика очереди и движка, принятые решения.

- Генерация демо: `make generate` (`go generate ./examples/demo`). После правки шаблонов генератора перегенерировать демо и закоммитить вместе.
- Тесты: `make test` — с Postgres из `GO_BPM_TEST_DSN` (по умолчанию `localhost/go_bpm_test`, база очищается), `make test-unit` — без базы.
- В `examples/demo/bpmgen/<процесс>/method_*.go`, `handlers/`, `model/`, `services/` без заголовка `Code generated` — рукописный код, не перезаписывать.
- Изменение контракта `bpm` = шаблоны `internal/generator/templates` + движок `engine` + перегенерация демо, в одном коммите.
- Модули с публичного прокси: `GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org`, если в окружении задан корпоративный.
- Комментарии и сообщения — по-русски, как в остальном коде.
