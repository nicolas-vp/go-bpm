package webui

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

// Страница истории /ui/history/?instanceId=<id>: диаграмма процесса (bpmn-js) с пройденными элементами экземпляра
// и таблицы решений DMN (dmn-js) со сработавшими правилами задач бизнес-правил

// DiagramService - чтение истории для страницы; *engine.HistoryReader подходит
type DiagramService interface {
	GetDiagram(ctx context.Context, instanceUid uuid.UUID) (*engine.Diagram, error)
	GetRulesHistory(ctx context.Context, instanceUid uuid.UUID, elementId string) (*engine.RulesHistory, error)
	GetUserTasks(ctx context.Context, instanceUid uuid.UUID, elementId string) ([]engine.UserTask, error)
}

// UserTasksResponse - задачи пользователя элемента: назначение, кандидаты, кто и когда выполнил
type UserTasksResponse struct {
	Tasks []engine.UserTask `json:"tasks"`
}

func diagramHandler(service DiagramService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.URL.Query().Get("instanceId"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "instanceId: "+err.Error())
			return
		}
		diagram, err := service.GetDiagram(r.Context(), instanceUid)
		if errors.Is(err, engine.ErrInstanceNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "history diagram", "instance", instanceUid, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, diagram)
	}
}

func rulesHandler(service DiagramService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.URL.Query().Get("instanceId"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "instanceId: "+err.Error())
			return
		}
		elementId := r.URL.Query().Get("elementId")
		if elementId == "" {
			writeError(w, http.StatusBadRequest, "elementId is required")
			return
		}
		history, err := service.GetRulesHistory(r.Context(), instanceUid, elementId)
		if err != nil {
			slog.ErrorContext(r.Context(), "rules history", "instance", instanceUid, "element", elementId, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, history)
	}
}

func userTasksHandler(service DiagramService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.URL.Query().Get("instanceId"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "instanceId: "+err.Error())
			return
		}
		elementId := r.URL.Query().Get("elementId")
		if elementId == "" {
			writeError(w, http.StatusBadRequest, "elementId is required")
			return
		}
		tasks, err := service.GetUserTasks(r.Context(), instanceUid, elementId)
		if err != nil {
			slog.ErrorContext(r.Context(), "history user tasks", "instance", instanceUid, "element", elementId, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, UserTasksResponse{Tasks: tasks})
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	// BPMN - XML, без экранирования < и > ответ короче и читается в devtools
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(body)
}
