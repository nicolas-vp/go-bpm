package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/nicolas-vp/go-bpm/engine"
)

// MessageCorrelator - передача сообщений ждущим токенам; *engine.Engine подходит
type MessageCorrelator interface {
	CorrelateMessage(ctx context.Context, name, correlationKey string, variables map[string]any, opts ...engine.MessageOption) (int, error)
}

// MessageRequest - тело передачи сообщения
type MessageRequest struct {
	// Name - имя сообщения, как у bpmn:message
	Name string `json:"name"`
	// CorrelationKey - ключ корреляции: бизнес-ключ экземпляра или значение переменной подписки
	CorrelationKey string         `json:"correlationKey"`
	Variables      map[string]any `json:"variables"`
	// TTL - сколько сообщение ждёт подписку, если его никто не ждёт, длительность Go ("10m"); пусто - не ждёт
	TTL string `json:"ttl"`
}

type MessageResponse struct {
	// Correlated - скольким токенам досталось сообщение; 0 - сообщение сохранено до подписки (ttl)
	Correlated int `json:"correlated"`
}

// RegisterMessages вешает на mux передачу сообщений:
//   - POST /api/v1/messages {"name", "correlationKey", "variables", "ttl"} - передать сообщение ждущим токенам.
//
// Отвечает 200 и числом токенов. Ошибки: 400 - неверный запрос, 404 - сообщение никто не ждёт, а ttl не задан
func RegisterMessages(mux *http.ServeMux, correlator MessageCorrelator) {
	mux.HandleFunc("POST /api/v1/messages", messageHandler(correlator))
}

func messageHandler(correlator MessageCorrelator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req MessageRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		var opts []engine.MessageOption
		if req.TTL != "" {
			ttl, err := time.ParseDuration(req.TTL)
			if err != nil || ttl <= 0 {
				writeError(w, http.StatusBadRequest, "ttl: positive Go duration expected, for example 10m")
				return
			}
			opts = append(opts, engine.WithMessageTTL(ttl))
		}
		correlated, err := correlator.CorrelateMessage(r.Context(), req.Name, req.CorrelationKey, req.Variables, opts...)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, MessageResponse{Correlated: correlated})
		case errors.Is(err, engine.ErrMessageRequired):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, engine.ErrMessageNotCorrelated):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			slog.ErrorContext(r.Context(), "correlate message", "message", req.Name, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
	}
}
