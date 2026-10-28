// Package httpapi - JSON API движка на net/http:
//   - GET /api/v1/processes - процессы движка с числом экземпляров по статусам;
//   - GET /api/v1/processes/{name} - процесс с BPMN;
//   - POST /api/v1/processes/{name}/instances - запустить экземпляр процесса, тело {"variables": {...}, "businessKey": "..."},
//     ответ {"id": "<instance id>"};
//   - GET /api/v1/processes/{name}/heatmap?from=&to=&limit= - термокарта: длительности элементов процесса по истории
//     экземпляров, запущенных в [from, to) (RFC 3339, необязательны), limit - сколько последних экземпляров взять;
//   - GET /api/v1/processes/{name}/stats?from=&to=&tz= - динамика: экземпляры по дням запуска и их исходы, медиана
//     и p95 длительности завершённых; по умолчанию последние 30 дней, дни режутся в часовом поясе tz (IANA, UTC);
//   - GET /api/v1/instances?processName=&status=&businessKey=&from=&to=&limit=&before= - экземпляры от последних
//     запущенных, from и to - когда запущен, [from, to) (RFC 3339, необязательны), before - id последнего экземпляра
//     предыдущей страницы;
//   - GET /api/v1/instances/{id} - экземпляр: тип процесса, бизнес-ключ, статус, когда запущен и обновлён;
//   - GET /api/v1/instances/{id}/history?withChildren=true - история экземпляра в порядке записи,
//     withChildren добавляет дочерние экземпляры (подпроцессы и callActivity) на всю глубину.
//
// Ошибки - {"error": "..."} с кодом 400 (неверный запрос), 404 (нет процесса или экземпляра) или 500.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

// maxBodySize - предел тела запроса старта процесса
const maxBodySize = 1 << 20

// ProcessStarter - процессы движка и их запуск; *engine.Engine подходит
type ProcessStarter interface {
	Processes() []engine.ProcessDefinition
	Process(id string) (*engine.ProcessDefinition, error)
	StartProcess(ctx context.Context, processName string, variables map[string]any, opts ...engine.StartOption) (uuid.UUID, error)
}

type HistoryReader interface {
	GetHistory(ctx context.Context, instanceUid uuid.UUID, withChildren bool) ([]engine.HistoryRecord, error)
	GetInstance(ctx context.Context, instanceUid uuid.UUID) (*engine.Instance, error)
	ListInstances(ctx context.Context, filter engine.InstanceFilter) ([]engine.Instance, error)
	CountInstances(ctx context.Context) ([]engine.InstanceCount, error)
	GetHeatmap(ctx context.Context, filter engine.HeatmapFilter) (*engine.Heatmap, error)
	GetProcessStats(ctx context.Context, filter engine.StatsFilter) (*engine.ProcessStats, error)
}

// defaultStatsPeriod - период динамики без from
const defaultStatsPeriod = 30 * 24 * time.Hour

// ProcessSummary - процесс в списке: Instances - число экземпляров по статусам, без дочерних встроенных подпроцессов
type ProcessSummary struct {
	engine.ProcessDefinition
	Instances map[string]int64 `json:"instances"`
}

type ProcessesResponse struct {
	Processes []ProcessSummary `json:"processes"`
}

// StartRequest - тело запроса старта процесса
type StartRequest struct {
	// Variables - переменные экземпляра; числа приходят как float64, как из любого JSON
	Variables map[string]any `json:"variables"`
	// BusinessKey - бизнес-ключ экземпляра, необязательный
	BusinessKey string `json:"businessKey"`
}

type StartResponse struct {
	Id uuid.UUID `json:"id"`
}

type HistoryResponse struct {
	Records []engine.HistoryRecord `json:"records"`
}

type InstancesResponse struct {
	Instances []engine.Instance `json:"instances"`
}

// Register вешает API на mux
func Register(mux *http.ServeMux, processes ProcessStarter, history HistoryReader) {
	mux.HandleFunc("GET /api/v1/processes", listProcessesHandler(processes, history))
	mux.HandleFunc("GET /api/v1/processes/{name}", processHandler(processes))
	mux.HandleFunc("POST /api/v1/processes/{name}/instances", startHandler(processes))
	mux.HandleFunc("GET /api/v1/processes/{name}/heatmap", heatmapHandler(history))
	mux.HandleFunc("GET /api/v1/processes/{name}/stats", statsHandler(history))
	mux.HandleFunc("GET /api/v1/instances", listInstancesHandler(history))
	mux.HandleFunc("GET /api/v1/instances/{id}", instanceHandler(history))
	mux.HandleFunc("GET /api/v1/instances/{id}/history", historyHandler(history))
}

func listProcessesHandler(processes ProcessStarter, history HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := history.CountInstances(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "count instances", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		byProcess := make(map[string]map[string]int64)
		for _, count := range counts {
			if byProcess[count.ProcessName] == nil {
				byProcess[count.ProcessName] = make(map[string]int64)
			}
			byProcess[count.ProcessName][count.Status] = count.Count
		}
		list := []ProcessSummary{}
		for _, definition := range processes.Processes() {
			instances := byProcess[definition.Id]
			if instances == nil {
				instances = map[string]int64{}
			}
			list = append(list, ProcessSummary{ProcessDefinition: definition, Instances: instances})
		}
		writeJSON(w, http.StatusOK, ProcessesResponse{Processes: list})
	}
}

func processHandler(processes ProcessStarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		definition, err := processes.Process(r.PathValue("name"))
		if errors.Is(err, engine.ErrProcessNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, definition)
	}
}

func startHandler(processes ProcessStarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req StartRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		var opts []engine.StartOption
		if req.BusinessKey != "" {
			opts = append(opts, engine.WithBusinessKey(req.BusinessKey))
		}
		id, err := processes.StartProcess(r.Context(), r.PathValue("name"), req.Variables, opts...)
		if errors.Is(err, engine.ErrProcessNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "start process", "process", r.PathValue("name"), "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, StartResponse{Id: id})
	}
}

func heatmapHandler(history HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		filter := engine.HeatmapFilter{ProcessName: r.PathValue("name")}
		for name, bound := range map[string]*time.Time{"from": &filter.From, "to": &filter.To} {
			if value := query.Get(name); value != "" {
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					writeError(w, http.StatusBadRequest, name+": "+err.Error())
					return
				}
				*bound = parsed
			}
		}
		if value := query.Get("limit"); value != "" {
			limit, err := strconv.Atoi(value)
			if err != nil || limit <= 0 {
				writeError(w, http.StatusBadRequest, "limit: positive integer expected")
				return
			}
			filter.Limit = limit
		}
		heatmap, err := history.GetHeatmap(r.Context(), filter)
		if errors.Is(err, engine.ErrProcessNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "heatmap", "process", filter.ProcessName, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, heatmap)
	}
}

func statsHandler(history HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		filter := engine.StatsFilter{ProcessName: r.PathValue("name"), To: time.Now(), Location: time.UTC}
		if value := query.Get("tz"); value != "" {
			location, err := time.LoadLocation(value)
			// Local - часовой пояс сервера, Postgres его не знает
			if err != nil || value == "Local" {
				writeError(w, http.StatusBadRequest, "tz: IANA time zone expected, e.g. Europe/Moscow")
				return
			}
			filter.Location = location
		}
		for name, bound := range map[string]*time.Time{"from": &filter.From, "to": &filter.To} {
			if value := query.Get(name); value != "" {
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					writeError(w, http.StatusBadRequest, name+": "+err.Error())
					return
				}
				*bound = parsed
			}
		}
		if filter.From.IsZero() {
			filter.From = filter.To.Add(-defaultStatsPeriod)
		}
		stats, err := history.GetProcessStats(r.Context(), filter)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, stats)
		case errors.Is(err, engine.ErrProcessNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, engine.ErrInvalidPeriod):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			slog.ErrorContext(r.Context(), "process stats", "process", filter.ProcessName, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
	}
}

func listInstancesHandler(history HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		filter := engine.InstanceFilter{
			ProcessName: query.Get("processName"),
			Status:      query.Get("status"),
			BusinessKey: query.Get("businessKey"),
		}
		for name, bound := range map[string]*time.Time{"from": &filter.From, "to": &filter.To} {
			if value := query.Get(name); value != "" {
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					writeError(w, http.StatusBadRequest, name+": "+err.Error())
					return
				}
				*bound = parsed
			}
		}
		if value := query.Get("limit"); value != "" {
			limit, err := strconv.Atoi(value)
			if err != nil || limit <= 0 {
				writeError(w, http.StatusBadRequest, "limit: positive integer expected")
				return
			}
			filter.Limit = limit
		}
		if value := query.Get("before"); value != "" {
			before, err := uuid.Parse(value)
			if err != nil {
				writeError(w, http.StatusBadRequest, "before: "+err.Error())
				return
			}
			filter.Before = before
		}
		instances, err := history.ListInstances(r.Context(), filter)
		if errors.Is(err, engine.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "status: "+err.Error())
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "list instances", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if instances == nil {
			instances = []engine.Instance{}
		}
		writeJSON(w, http.StatusOK, InstancesResponse{Instances: instances})
	}
}

func instanceHandler(history HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "id: "+err.Error())
			return
		}
		instance, err := history.GetInstance(r.Context(), instanceUid)
		if errors.Is(err, engine.ErrInstanceNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "get instance", "instance", instanceUid, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, instance)
	}
}

func historyHandler(history HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "id: "+err.Error())
			return
		}
		withChildren := false
		if value := r.URL.Query().Get("withChildren"); value != "" {
			if withChildren, err = strconv.ParseBool(value); err != nil {
				writeError(w, http.StatusBadRequest, "withChildren: "+err.Error())
				return
			}
		}
		records, err := history.GetHistory(r.Context(), instanceUid, withChildren)
		if err != nil {
			slog.ErrorContext(r.Context(), "get history", "instance", instanceUid, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if len(records) == 0 {
			writeError(w, http.StatusNotFound, engine.ErrInstanceNotFound.Error())
			return
		}
		writeJSON(w, http.StatusOK, HistoryResponse{Records: records})
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
