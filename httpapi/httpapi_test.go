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
	id          uuid.UUID
	variables   map[string]any
	businessKey string
	filter      engine.InstanceFilter
}

func (f *fakeEngine) StartProcess(_ context.Context, processName string, variables map[string]any, opts ...engine.StartOption) (uuid.UUID, error) {
	if processName != "main" {
		return uuid.Nil, fmt.Errorf("process %q: %w", processName, engine.ErrProcessNotFound)
	}
	f.variables = variables
	f.businessKey = engine.ApplyStartOptions(opts...).BusinessKey
	return f.id, nil
}

func (f *fakeEngine) Processes() []engine.ProcessDefinition {
	return []engine.ProcessDefinition{{Id: "main", Name: "Главный"}, {Id: "idle"}}
}

func (f *fakeEngine) Process(id string) (*engine.ProcessDefinition, error) {
	if id != "main" {
		return nil, fmt.Errorf("process %q: %w", id, engine.ErrProcessNotFound)
	}
	return &engine.ProcessDefinition{Id: "main", Name: "Главный", BPMN: "<definitions/>"}, nil
}

func (f *fakeEngine) CountInstances(context.Context) ([]engine.InstanceCount, error) {
	return []engine.InstanceCount{{ProcessName: "main", Status: engine.InstanceRunning, Count: 2}, {ProcessName: "main.sub", Status: engine.InstanceRunning, Count: 1}}, nil
}

func (f *fakeEngine) GetInstance(_ context.Context, instanceUid uuid.UUID) (*engine.Instance, error) {
	if instanceUid != f.id {
		return nil, fmt.Errorf("instance %s: %w", instanceUid, engine.ErrInstanceNotFound)
	}
	return &engine.Instance{InstanceUid: instanceUid, ProcessName: "main", BusinessKey: "order-1", Status: engine.InstanceRunning}, nil
}

func (f *fakeEngine) ListInstances(_ context.Context, filter engine.InstanceFilter) ([]engine.Instance, error) {
	if filter.Status == "unknown" {
		return nil, engine.ErrInvalidStatus
	}
	f.filter = filter
	if filter.ProcessName != "main" {
		return nil, nil
	}
	return []engine.Instance{{InstanceUid: f.id, ProcessName: "main", Status: engine.InstanceRunning}}, nil
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
		{method: "POST", path: "/api/v1/processes/main/instances", body: `{"variables":{"amount":55,"name":"x"},"businessKey":"order-1"}`,
			wantStatus: http.StatusCreated, wantBody: fake.id.String()},
		{method: "POST", path: "/api/v1/processes/other/instances", body: `{}`, wantStatus: http.StatusNotFound, wantBody: "process not found"},
		{method: "POST", path: "/api/v1/processes/main/instances", body: `{`, wantStatus: http.StatusBadRequest, wantBody: "body"},
		{method: "GET", path: "/api/v1/processes/main/instances", wantStatus: http.StatusMethodNotAllowed},
		{method: "GET", path: "/api/v1/processes", wantStatus: http.StatusOK,
			wantBody: `{"processes":[{"id":"main","name":"Главный","instances":{"running":2}},{"id":"idle","instances":{}}]}`},
		{method: "GET", path: "/api/v1/processes/main", wantStatus: http.StatusOK, wantBody: `"bpmn":"\u003cdefinitions/\u003e"`},
		{method: "GET", path: "/api/v1/processes/other", wantStatus: http.StatusNotFound, wantBody: "process not found"},
		{method: "GET", path: "/api/v1/instances/" + fake.id.String() + "/history", wantStatus: http.StatusOK, wantBody: `"element_id":"start"`},
		{method: "GET", path: "/api/v1/instances/" + fake.id.String() + "/history?withChildren=true", wantStatus: http.StatusOK, wantBody: `"element_id":"child"`},
		{method: "GET", path: "/api/v1/instances/" + fake.id.String() + "/history?withChildren=maybe", wantStatus: http.StatusBadRequest, wantBody: "withChildren"},
		{method: "GET", path: "/api/v1/instances/" + uuid.NewString() + "/history", wantStatus: http.StatusNotFound, wantBody: "instance not found"},
		{method: "GET", path: "/api/v1/instances/bad/history", wantStatus: http.StatusBadRequest, wantBody: "id"},
		{method: "GET", path: "/api/v1/instances/" + fake.id.String(), wantStatus: http.StatusOK, wantBody: `"business_key":"order-1"`},
		{method: "GET", path: "/api/v1/instances/" + uuid.NewString(), wantStatus: http.StatusNotFound, wantBody: "instance not found"},
		{method: "GET", path: "/api/v1/instances/bad", wantStatus: http.StatusBadRequest, wantBody: "id"},
		{method: "GET", path: "/api/v1/instances?processName=other", wantStatus: http.StatusOK, wantBody: `{"instances":[]}`},
		{method: "GET", path: "/api/v1/instances?status=unknown", wantStatus: http.StatusBadRequest, wantBody: "status"},
		{method: "GET", path: "/api/v1/instances?limit=0", wantStatus: http.StatusBadRequest, wantBody: "limit"},
		{method: "GET", path: "/api/v1/instances?before=bad", wantStatus: http.StatusBadRequest, wantBody: "before"},
		{method: "GET", path: "/api/v1/instances?processName=main&status=running&businessKey=order-1&limit=5&before=" + fake.id.String(),
			wantStatus: http.StatusOK, wantBody: `"status":"running"`},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s %s: %d %.200q, want %d containing %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
		}
	}
	if fake.variables["amount"] != float64(55) || fake.variables["name"] != "x" || fake.businessKey != "order-1" {
		t.Errorf("variables = %v, business key = %q", fake.variables, fake.businessKey)
	}
	wantFilter := engine.InstanceFilter{ProcessName: "main", Status: engine.InstanceRunning, BusinessKey: "order-1", Before: fake.id, Limit: 5}
	if fake.filter != wantFilter {
		t.Errorf("filter = %+v, want %+v", fake.filter, wantFilter)
	}
}
