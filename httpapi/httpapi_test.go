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

type fakeEngine struct {
	id        uuid.UUID
	variables map[string]any
}

func (f *fakeEngine) StartProcess(_ context.Context, processName string, variables map[string]any) (uuid.UUID, error) {
	if processName != "main" {
		return uuid.Nil, fmt.Errorf("process %q: %w", processName, engine.ErrProcessNotFound)
	}
	f.variables = variables
	return f.id, nil
}

func (f *fakeEngine) GetHistory(_ context.Context, instanceUid uuid.UUID, withChildren bool) ([]engine.HistoryRecord, error) {
	if instanceUid != f.id {
		return nil, nil
	}
	records := []engine.HistoryRecord{{Event: engine.HistoryCompleted, InstanceUid: instanceUid, ElementId: "start"}}
	if withChildren {
		records = append(records, engine.HistoryRecord{Event: engine.HistoryCompleted, ElementId: "child"})
	}
	return records, nil
}

func TestAPI(t *testing.T) {
	fake := &fakeEngine{id: uuid.New()}
	mux := http.NewServeMux()
	Register(mux, fake, fake)

	for _, tc := range []struct {
		method, path, body, wantBody string
		wantStatus                   int
	}{
		{method: "POST", path: "/api/v1/processes/main/instances", body: `{"variables":{"amount":55,"name":"x"}}`,
			wantStatus: http.StatusCreated, wantBody: fake.id.String()},
		{method: "POST", path: "/api/v1/processes/other/instances", body: `{}`, wantStatus: http.StatusNotFound, wantBody: "process not found"},
		{method: "POST", path: "/api/v1/processes/main/instances", body: `{`, wantStatus: http.StatusBadRequest, wantBody: "body"},
		{method: "GET", path: "/api/v1/processes/main/instances", wantStatus: http.StatusMethodNotAllowed},
		{method: "GET", path: "/api/v1/instances/" + fake.id.String() + "/history", wantStatus: http.StatusOK, wantBody: `"element_id":"start"`},
		{method: "GET", path: "/api/v1/instances/" + fake.id.String() + "/history?withChildren=true", wantStatus: http.StatusOK, wantBody: `"element_id":"child"`},
		{method: "GET", path: "/api/v1/instances/" + fake.id.String() + "/history?withChildren=maybe", wantStatus: http.StatusBadRequest, wantBody: "withChildren"},
		{method: "GET", path: "/api/v1/instances/" + uuid.NewString() + "/history", wantStatus: http.StatusNotFound, wantBody: "instance not found"},
		{method: "GET", path: "/api/v1/instances/bad/history", wantStatus: http.StatusBadRequest, wantBody: "id"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s %s: %d %.200q, want %d containing %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
		}
	}
	if fake.variables["amount"] != float64(55) || fake.variables["name"] != "x" {
		t.Errorf("variables = %v", fake.variables)
	}
}
