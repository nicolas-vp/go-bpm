# Образ демо-сервиса go-bpm: JSON API и консоль /ui/ на порту 8080. Статика консоли и миграции вшиты в бинарник,
# поэтому в образе только он. База - PostgreSQL из GO_BPM_DSN, миграции применяются при старте.
#
#	docker build -t go-bpm .
#	docker run -p 8080:8080 -e GO_BPM_DSN=postgres://user:pass@host:5432/go_bpm?sslmode=disable go-bpm

FROM golang:1.26-alpine AS build
WORKDIR /src
# модули отдельным слоем: при правке кода они не скачиваются заново
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/go-bpm-demo ./examples/demo/cmd/demo

# distroless: без shell и пакетного менеджера, пользователь nonroot
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/go-bpm-demo /go-bpm-demo
ENV GO_BPM_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/go-bpm-demo"]
