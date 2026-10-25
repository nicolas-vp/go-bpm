// Package httpapi - JSON API движка на net/http:
//   - POST /api/v1/processes/{name}/instances - запустить экземпляр процесса, тело {"variables": {...}},
//     ответ {"id": "<instance id>"};
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

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

// maxBodySize - предел тела запроса старта процесса
const maxBodySize = 1 << 20

type ProcessStarter interface {
	StartProcess(ctx context.Context, processName string, variables map[string]any) (uuid.UUID, error)
}

type HistoryReader interface {
	GetHistory(ctx context.Context, instanceUid uuid.UUID, withChildren bool) ([]engine.HistoryRecord, error)
}

// StartRequest - тело запроса старта процесса
type StartRequest struct {
	// Variables - переменные экземпляра; числа приходят как float64, как из любого JSON
	Variables map[string]any `json:"variables"`
}

type StartResponse struct {
	Id uuid.UUID `json:"id"`
}

type HistoryResponse struct {
	Records []engine.HistoryRecord `json:"records"`
}

// Register вешает API на mux
func Register(mux *http.ServeMux, processes ProcessStarter, history HistoryReader) {
	mux.HandleFunc("POST /api/v1/processes/{name}/instances", startHandler(processes))
	mux.HandleFunc("GET /api/v1/instances/{id}/history", historyHandler(history))
}

func startHandler(processes ProcessStarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req StartRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		id, err := processes.StartProcess(r.Context(), r.PathValue("name"), req.Variables)
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
