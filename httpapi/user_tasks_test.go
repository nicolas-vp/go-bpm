package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

// fakeTasks - одна задача id, взять её может bob из managers, завершить - только bob
type fakeTasks struct {
	id        uuid.UUID
	filter    engine.TaskFilter
	variables map[string]any
}

func (f *fakeTasks) check(taskUid uuid.UUID) error {
	if taskUid != f.id {
		return fmt.Errorf("user task %s: %w", taskUid, engine.ErrTaskNotFound)
	}
	return nil
}

func (f *fakeTasks) GetTask(_ context.Context, taskUid uuid.UUID) (*engine.UserTask, error) {
	if err := f.check(taskUid); err != nil {
		return nil, err
	}
	return &engine.UserTask{TaskUid: taskUid, Name: "Согласовать", Status: engine.UserTaskCreated, Variables: map[string]any{"amount": 5}}, nil
}

func (f *fakeTasks) ListTasks(_ context.Context, filter engine.TaskFilter) ([]engine.UserTask, error) {
	if filter.Status == "open" {
		return nil, engine.ErrInvalidTaskStatus
	}
	f.filter = filter
	return nil, nil
}

func (f *fakeTasks) Claim(_ context.Context, taskUid uuid.UUID, userId string, groups []string) error {
	if err := f.check(taskUid); err != nil {
		return err
	}
	switch {
	case userId == "":
		return engine.ErrUserRequired
	case userId == "dave":
		return engine.ErrTaskClaimed
	case userId != "bob" || !reflect.DeepEqual(groups, []string{"managers"}):
		return engine.ErrNotCandidate
	}
	return nil
}

func (f *fakeTasks) Unclaim(_ context.Context, taskUid uuid.UUID, userId string) error {
	if err := f.check(taskUid); err != nil {
		return err
	}
	if userId != "bob" {
		return engine.ErrNotAssignee
	}
	return nil
}

func (f *fakeTasks) Complete(_ context.Context, taskUid uuid.UUID, userId string, variables map[string]any) error {
	if err := f.check(taskUid); err != nil {
		return err
	}
	if userId != "bob" {
		return engine.ErrNotAssignee
	}
	f.variables = variables
	return nil
}

func TestUserTasksAPI(t *testing.T) {
	fake := &fakeTasks{id: uuid.New()}
	mux := http.NewServeMux()
	RegisterUserTasks(mux, fake)
	task := "/api/v1/tasks/" + fake.id.String()

	for _, tc := range []struct {
		method, path, body, wantBody string
		wantStatus                   int
	}{
		{method: "GET", path: "/api/v1/tasks?assignee=bob&candidateUser=bob&candidateGroup=managers,admins&candidateGroup=auditors&processName=p&limit=10",
			wantStatus: http.StatusOK, wantBody: `{"tasks":[]}`},
		{method: "GET", path: "/api/v1/tasks?status=open", wantStatus: http.StatusBadRequest, wantBody: "status"},
		{method: "GET", path: "/api/v1/tasks?instanceId=bad", wantStatus: http.StatusBadRequest, wantBody: "instanceId"},
		{method: "GET", path: "/api/v1/tasks?limit=-1", wantStatus: http.StatusBadRequest, wantBody: "limit"},
		{method: "GET", path: task, wantStatus: http.StatusOK, wantBody: `"variables":{"amount":5}`},
		{method: "GET", path: "/api/v1/tasks/" + uuid.NewString(), wantStatus: http.StatusNotFound, wantBody: "not found"},
		{method: "GET", path: "/api/v1/tasks/bad", wantStatus: http.StatusBadRequest, wantBody: "id"},
		{method: "POST", path: task + "/claim", body: `{"userId":"bob","groups":["managers"]}`, wantStatus: http.StatusNoContent},
		{method: "POST", path: task + "/claim", body: `{"userId":"carol","groups":["accounting"]}`, wantStatus: http.StatusForbidden, wantBody: "not a candidate"},
		{method: "POST", path: task + "/claim", body: `{"userId":"dave"}`, wantStatus: http.StatusConflict, wantBody: "claimed"},
		{method: "POST", path: task + "/claim", body: `{}`, wantStatus: http.StatusBadRequest, wantBody: "userId"},
		{method: "POST", path: task + "/claim", body: `{`, wantStatus: http.StatusBadRequest, wantBody: "body"},
		{method: "POST", path: task + "/unclaim", body: `{"userId":"dave"}`, wantStatus: http.StatusConflict, wantBody: "not assigned"},
		{method: "POST", path: task + "/unclaim", body: `{"userId":"bob"}`, wantStatus: http.StatusNoContent},
		{method: "POST", path: task + "/complete", body: `{"userId":"carol"}`, wantStatus: http.StatusConflict, wantBody: "not assigned"},
		{method: "POST", path: task + "/complete", body: `{"userId":"bob","variables":{"approved":true}}`, wantStatus: http.StatusNoContent},
		{method: "POST", path: "/api/v1/tasks/" + uuid.NewString() + "/complete", body: `{"userId":"bob"}`, wantStatus: http.StatusNotFound},
		{method: "GET", path: task + "/complete", wantStatus: http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s %s: %d %.200q, want %d containing %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
		}
	}
	wantFilter := engine.TaskFilter{Assignee: "bob", CandidateUser: "bob", CandidateGroups: []string{"managers", "admins", "auditors"}, ProcessName: "p", Limit: 10}
	if !reflect.DeepEqual(fake.filter, wantFilter) {
		t.Errorf("filter = %+v, want %+v", fake.filter, wantFilter)
	}
	if fake.variables["approved"] != true {
		t.Errorf("complete variables = %v", fake.variables)
	}
}
