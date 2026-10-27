package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

type fakeOperations struct {
	root, child uuid.UUID
	retried     []string
}

func (f *fakeOperations) ListFailures(_ context.Context, instanceUid uuid.UUID) ([]engine.Failure, error) {
	if instanceUid != f.root {
		return nil, nil
	}
	return []engine.Failure{{TaskId: 7, InstanceUid: f.child, ElementId: "check", Error: "boom"}}, nil
}

func (f *fakeOperations) Retry(_ context.Context, taskId int64, variables map[string]any) error {
	if taskId != 7 {
		return fmt.Errorf("task %d: %w", taskId, engine.ErrFailureNotFound)
	}
	f.retried = append(f.retried, fmt.Sprint(variables))
	return nil
}

func (f *fakeOperations) Cancel(_ context.Context, instanceUid uuid.UUID) error {
	switch instanceUid {
	case f.root:
		return nil
	case f.child:
		return engine.ErrNotRootInstance
	}
	return engine.ErrInstanceNotFound
}

func TestOperationsAPI(t *testing.T) {
	fake := &fakeOperations{root: uuid.New(), child: uuid.New()}
	mux := http.NewServeMux()
	RegisterOperations(mux, fake)

	for _, tc := range []struct {
		method, path, body, wantBody string
		wantStatus                   int
	}{
		{method: "GET", path: "/api/v1/instances/" + fake.root.String() + "/failures", wantStatus: http.StatusOK, wantBody: `"task_id":7`},
		{method: "GET", path: "/api/v1/instances/" + uuid.NewString() + "/failures", wantStatus: http.StatusOK, wantBody: `{"failures":[]}`},
		{method: "GET", path: "/api/v1/instances/bad/failures", wantStatus: http.StatusBadRequest, wantBody: "id"},
		{method: "POST", path: "/api/v1/failures/7/retry", wantStatus: http.StatusNoContent},
		{method: "POST", path: "/api/v1/failures/7/retry", body: `{"variables":{"ok":true}}`, wantStatus: http.StatusNoContent},
		{method: "POST", path: "/api/v1/failures/8/retry", body: `{}`, wantStatus: http.StatusNotFound, wantBody: "failed task not found"},
		{method: "POST", path: "/api/v1/failures/x/retry", wantStatus: http.StatusBadRequest, wantBody: "taskId"},
		{method: "POST", path: "/api/v1/failures/7/retry", body: `{`, wantStatus: http.StatusBadRequest, wantBody: "body"},
		{method: "POST", path: "/api/v1/instances/" + fake.root.String() + "/cancel", wantStatus: http.StatusNoContent},
		{method: "POST", path: "/api/v1/instances/" + fake.child.String() + "/cancel", wantStatus: http.StatusConflict, wantBody: "not a root"},
		{method: "POST", path: "/api/v1/instances/" + uuid.NewString() + "/cancel", wantStatus: http.StatusNotFound, wantBody: "instance not found"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s %s: %d %.200q, want %d containing %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
		}
	}
	if want := "map[] map[ok:true]"; strings.Join(fake.retried, " ") != want {
		t.Errorf("retried = %q, want %q", strings.Join(fake.retried, " "), want)
	}
}
