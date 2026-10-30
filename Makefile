# Тестовая база очищается тестами, не направляйте её на рабочую
GO_BPM_TEST_DSN ?= postgres://postgres:postgres@localhost:5432/go_bpm_test?sslmode=disable
GO_BPM_DSN ?= postgres://postgres:postgres@localhost:5432/go_bpm?sslmode=disable

.PHONY: build test test-unit generate run lint vulncheck docker

build:
	go build ./...

# тесты с базой делят таблицы, поэтому пакеты гоняются по одному (-p 1)
test:
	GO_BPM_TEST_DSN="$(GO_BPM_TEST_DSN)" go test -race -count=1 -p 1 ./...

# без базы: интеграционные тесты пропускаются
test-unit:
	go test -race -count=1 ./...

# перегенерировать демо-процессы из examples/demo/process
generate:
	go generate ./examples/demo

run:
	GO_BPM_DSN="$(GO_BPM_DSN)" go run ./examples/demo/cmd/demo

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"

# известные уязвимости в вызываемом коде модулей и стандартной библиотеки установленного Go
vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

# образ демо-сервиса, как его собирает CI (ghcr.io/nicolas-vp/go-bpm)
docker:
	docker build -t go-bpm .
