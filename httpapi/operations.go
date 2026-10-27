package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

// InstanceOperations - упавшие задачи экземпляров, перезапуск и отмена; *engine.InstanceOperations подходит
type InstanceOperations interface {
	ListFailures(ctx context.Context, instanceUid uuid.UUID) ([]engine.Failure, error)
	Retry(ctx context.Context, taskId int64, variables map[string]any) error
	Cancel(ctx context.Context, instanceUid uuid.UUID) error
}

// RetryRequest - тело перезапуска упавшей задачи
type RetryRequest struct {
	// Variables - заменить ими переменные токена; нет поля или null - перезапустить с прежними
	Variables map[string]any `json:"variables"`
}

type FailuresResponse struct {
	Failures []engine.Failure `json:"failures"`
}

// RegisterOperations вешает на mux операции поддержки над экземплярами:
//   - GET /api/v1/instances/{id}/failures - упавшие задачи экземпляра и его дочерних экземпляров;
//   - POST /api/v1/failures/{taskId}/retry {"variables": {...}} - перезапустить упавшую задачу, переменные необязательны;
//   - POST /api/v1/instances/{id}/cancel - отменить корневой экземпляр вместе с дочерними.
//
// retry и cancel отвечают 204. Ошибки: 400 - неверный запрос, 404 - нет упавшей задачи или экземпляра,
// 409 - экземпляр не корневой или уже завершён
func RegisterOperations(mux *http.ServeMux, operations InstanceOperations) {
	mux.HandleFunc("GET /api/v1/instances/{id}/failures", failuresHandler(operations))
	mux.HandleFunc("POST /api/v1/failures/{taskId}/retry", retryHandler(operations))
	mux.HandleFunc("POST /api/v1/instances/{id}/cancel", cancelHandler(operations))
}

func failuresHandler(operations InstanceOperations) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "id: "+err.Error())
			return
		}
		failures, err := operations.ListFailures(r.Context(), instanceUid)
		if err != nil {
			slog.ErrorContext(r.Context(), "list failures", "instance", instanceUid, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if failures == nil {
			failures = []engine.Failure{}
		}
		writeJSON(w, http.StatusOK, FailuresResponse{Failures: failures})
	}
}

func retryHandler(operations InstanceOperations) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskId, err := strconv.ParseInt(r.PathValue("taskId"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "taskId: "+err.Error())
			return
		}
		var req RetryRequest
		// тело необязательно: перезапуск с прежними переменными
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		err = operations.Retry(r.Context(), taskId, req.Variables)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, engine.ErrFailureNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			slog.ErrorContext(r.Context(), "retry failed task", "task", taskId, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
	}
}

func cancelHandler(operations InstanceOperations) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "id: "+err.Error())
			return
		}
		err = operations.Cancel(r.Context(), instanceUid)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, engine.ErrInstanceNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, engine.ErrNotRootInstance), errors.Is(err, engine.ErrInstanceFinished):
			writeError(w, http.StatusConflict, err.Error())
		default:
			slog.ErrorContext(r.Context(), "cancel instance", "instance", instanceUid, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
	}
}
