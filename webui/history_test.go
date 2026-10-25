package webui

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

type fakeDiagrams struct {
	known uuid.UUID
}

func (f fakeDiagrams) GetDiagram(_ context.Context, instanceUid uuid.UUID) (*engine.Diagram, error) {
	if instanceUid != f.known {
		return nil, fmt.Errorf("instance %s: %w", instanceUid, engine.ErrInstanceNotFound)
	}
	return &engine.Diagram{InstanceUid: instanceUid, ProcessName: "main", BPMN: "<definitions/>"}, nil
}

func (f fakeDiagrams) GetRulesHistory(_ context.Context, instanceUid uuid.UUID, elementId string) (*engine.RulesHistory, error) {
	history := &engine.RulesHistory{Records: []engine.RulesHistoryRecord{}, DMN: map[string]string{}}
	if instanceUid == f.known && elementId == "rule" {
		history.Records = append(history.Records, engine.RulesHistoryRecord{DecisionId: "D", RuleIds: []string{"r1"}})
		history.DMN["D"] = "<dmn/>"
	}
	return history, nil
}

func (f fakeDiagrams) GetUserTasks(_ context.Context, instanceUid uuid.UUID, elementId string) ([]engine.UserTask, error) {
	tasks := []engine.UserTask{}
	if instanceUid == f.known && elementId == "approve" {
		tasks = append(tasks, engine.UserTask{ElementId: "approve", Assignee: "bob", CandidateGroups: []string{"managers"}, Status: engine.UserTaskCreated})
	}
	return tasks, nil
}

func TestHistory(t *testing.T) {
	known := uuid.New()
	router := http.NewServeMux()
	Register(router, fakeDiagrams{known: known})

	for _, tc := range []struct {
		path, wantBody string
		wantStatus     int
	}{
		{path: "/history?instanceId=x", wantStatus: http.StatusMovedPermanently},
		{path: "/history/?instanceId=x", wantStatus: http.StatusMovedPermanently},
		{path: "/ui/history?instanceId=x", wantStatus: http.StatusMovedPermanently},
		{path: "/ui/history/", wantStatus: http.StatusOK, wantBody: "bpmn-navigated-viewer.production.min.js"},
		{path: "/ui/static/history.js", wantStatus: http.StatusOK, wantBody: "new BpmnJS"},
		{path: "/ui/static/vendor/bpmn-js-18.31.0/bpmn-js.css", wantStatus: http.StatusOK},
		{path: "/ui/history/api/diagram?instanceId=" + known.String(), wantStatus: http.StatusOK, wantBody: `"bpmn":"<definitions/>"`},
		{path: "/ui/history/api/diagram?instanceId=" + uuid.NewString(), wantStatus: http.StatusNotFound, wantBody: "instance not found"},
		{path: "/ui/history/api/diagram?instanceId=bad", wantStatus: http.StatusBadRequest, wantBody: "instanceId"},
		{path: "/ui/history/api/rules?instanceId=" + known.String() + "&elementId=rule", wantStatus: http.StatusOK, wantBody: `"rule_ids":["r1"]`},
		{path: "/ui/history/api/rules?instanceId=" + known.String() + "&elementId=other", wantStatus: http.StatusOK, wantBody: `"records":[]`},
		{path: "/ui/history/api/rules?instanceId=" + known.String(), wantStatus: http.StatusBadRequest, wantBody: "elementId"},
		{path: "/ui/static/vendor/dmn-js-17.12.0/dmn-viewer.production.min.js", wantStatus: http.StatusOK},
		{path: "/ui/history/api/tasks?instanceId=" + known.String() + "&elementId=approve", wantStatus: http.StatusOK, wantBody: `"assignee":"bob"`},
		{path: "/ui/history/api/tasks?instanceId=" + known.String() + "&elementId=other", wantStatus: http.StatusOK, wantBody: `{"tasks":[]}`},
		{path: "/ui/history/api/tasks?instanceId=" + known.String(), wantStatus: http.StatusBadRequest, wantBody: "elementId"},
		{path: "/ui/history/api/tasks?instanceId=bad&elementId=approve", wantStatus: http.StatusBadRequest, wantBody: "instanceId"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("GET %s: %d %.120q, want %d containing %q", tc.path, rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
		}
		if strings.HasPrefix(tc.path, "/ui/static/") || tc.path == "/ui/history/" {
			want := "no-cache"
			if strings.Contains(tc.path, "/vendor/") {
				want = "public, max-age=31536000, immutable"
			}
			if got := rec.Header().Get("Cache-Control"); got != want {
				t.Errorf("GET %s: Cache-Control = %q, want %q", tc.path, got, want)
			}
		}
		// старые ссылки /history/ ведут в историю консоли с теми же параметрами
		if tc.wantStatus == http.StatusMovedPermanently && rec.Header().Get("Location") != "/ui/history/?instanceId=x" {
			t.Errorf("GET %s: Location = %q", tc.path, rec.Header().Get("Location"))
		}
	}
}
