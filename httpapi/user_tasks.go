package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

// UserTasks - задачи пользователей; *engine.UserTaskService подходит
type UserTasks interface {
	GetTask(ctx context.Context, taskUid uuid.UUID) (*engine.UserTask, error)
	ListTasks(ctx context.Context, filter engine.TaskFilter) ([]engine.UserTask, error)
	Claim(ctx context.Context, taskUid uuid.UUID, userId string, groups []string) error
	Unclaim(ctx context.Context, taskUid uuid.UUID, userId string) error
	Complete(ctx context.Context, taskUid uuid.UUID, userId string, variables map[string]any) error
}

// TaskActionRequest - тело claim, unclaim и complete
type TaskActionRequest struct {
	UserId string `json:"userId"`
	// Groups - для claim: группы пользователя, по ним проверяется candidateGroups задачи
	Groups []string `json:"groups"`
	// Variables - для complete: переменные из формы, добавляются к переменным процесса
	Variables map[string]any `json:"variables"`
}

type TasksResponse struct {
	Tasks []engine.UserTask `json:"tasks"`
}

// RegisterUserTasks вешает API задач пользователей на mux:
//   - GET /api/v1/tasks?assignee=&candidateUser=&candidateGroup=&processName=&instanceId=&businessKey=&status=&limit=&before= -
//     задачи от последних созданных. assignee - назначенные пользователю, candidateUser и candidateGroup (можно
//     повторять или через запятую) - ещё не взятые, которые он может взять; вместе - «мои и доступные мне».
//     status - created (по умолчанию) или completed;
//   - GET /api/v1/tasks/{id} - задача с переменными процесса для формы;
//   - POST /api/v1/tasks/{id}/claim {"userId": "...", "groups": [...]} - взять в работу;
//   - POST /api/v1/tasks/{id}/unclaim {"userId": "..."} - вернуть в очередь;
//   - POST /api/v1/tasks/{id}/complete {"userId": "...", "variables": {...}} - завершить, только исполнитель.
//
// claim, unclaim и complete отвечают 204. Ошибки: 400 - неверный запрос, 403 - пользователь не кандидат,
// 404 - нет задачи, 409 - задача завершена, взята другим или не назначена пользователю
func RegisterUserTasks(mux *http.ServeMux, tasks UserTasks) {
	mux.HandleFunc("GET /api/v1/tasks", listTasksHandler(tasks))
	mux.HandleFunc("GET /api/v1/tasks/{id}", taskHandler(tasks))
	mux.HandleFunc("POST /api/v1/tasks/{id}/claim", taskActionHandler(func(ctx context.Context, id uuid.UUID, req TaskActionRequest) error {
		return tasks.Claim(ctx, id, req.UserId, req.Groups)
	}))
	mux.HandleFunc("POST /api/v1/tasks/{id}/unclaim", taskActionHandler(func(ctx context.Context, id uuid.UUID, req TaskActionRequest) error {
		return tasks.Unclaim(ctx, id, req.UserId)
	}))
	mux.HandleFunc("POST /api/v1/tasks/{id}/complete", taskActionHandler(func(ctx context.Context, id uuid.UUID, req TaskActionRequest) error {
		return tasks.Complete(ctx, id, req.UserId, req.Variables)
	}))
}

func listTasksHandler(tasks UserTasks) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		filter := engine.TaskFilter{
			Assignee:      query.Get("assignee"),
			CandidateUser: query.Get("candidateUser"),
			ProcessName:   query.Get("processName"),
			BusinessKey:   query.Get("businessKey"),
			Status:        query.Get("status"),
		}
		for _, value := range query["candidateGroup"] {
			for _, group := range strings.Split(value, ",") {
				if group = strings.TrimSpace(group); group != "" {
					filter.CandidateGroups = append(filter.CandidateGroups, group)
				}
			}
		}
		for name, target := range map[string]*uuid.UUID{"instanceId": &filter.InstanceUid, "before": &filter.Before} {
			if value := query.Get(name); value != "" {
				id, err := uuid.Parse(value)
				if err != nil {
					writeError(w, http.StatusBadRequest, name+": "+err.Error())
					return
				}
				*target = id
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
		list, err := tasks.ListTasks(r.Context(), filter)
		if errors.Is(err, engine.ErrInvalidTaskStatus) {
			writeError(w, http.StatusBadRequest, "status: "+err.Error())
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "list user tasks", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if list == nil {
			list = []engine.UserTask{}
		}
		writeJSON(w, http.StatusOK, TasksResponse{Tasks: list})
	}
}

func taskHandler(tasks UserTasks) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskUid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "id: "+err.Error())
			return
		}
		task, err := tasks.GetTask(r.Context(), taskUid)
		if err != nil {
			writeTaskError(w, r, taskUid, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

func taskActionHandler(action func(ctx context.Context, id uuid.UUID, req TaskActionRequest) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskUid, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "id: "+err.Error())
			return
		}
		var req TaskActionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		if err := action(r.Context(), taskUid, req); err != nil {
			writeTaskError(w, r, taskUid, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeTaskError(w http.ResponseWriter, r *http.Request, taskUid uuid.UUID, err error) {
	switch {
	case errors.Is(err, engine.ErrUserRequired):
		writeError(w, http.StatusBadRequest, "userId: "+err.Error())
	case errors.Is(err, engine.ErrNotCandidate):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, engine.ErrTaskNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, engine.ErrTaskCompleted), errors.Is(err, engine.ErrTaskClaimed), errors.Is(err, engine.ErrNotAssignee):
		writeError(w, http.StatusConflict, err.Error())
	default:
		slog.ErrorContext(r.Context(), "user task", "task", taskUid, "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
