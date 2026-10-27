package engine

import (
	"testing"
	"time"

	"github.com/nicolas-vp/go-bpm/bpm"
)

func TestHeatmapBuilder(t *testing.T) {
	// start -> f1 -> method -> f2 -> service -> f3 -> end; у service граничный таймер boundary -> f4 -> end;
	// встроенный подпроцесс main.sub: subStart -> f5 -> subEnd
	process := bpm.Process{Flows: map[string]bpm.DeploymentProcess{
		"main": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start":    {NextElements: []string{"f1"}},
			"f1":       {NextElements: []string{"method"}},
			"method":   {NextElements: []string{"f2"}},
			"f2":       {NextElements: []string{"service"}},
			"service":  {NextElements: []string{"f3"}, Boundaries: []bpm.Boundary{{ElementId: "boundary"}}},
			"f3":       {NextElements: []string{"end"}},
			"boundary": {NextElements: []string{"f4"}},
			"f4":       {NextElements: []string{"end"}},
			"end":      {},
		}},
		"main.sub": {StartElement: "subStart", Expressions: map[string]*bpm.Expression{
			"subStart": {NextElements: []string{"f5"}},
			"f5":       {NextElements: []string{"subEnd"}},
		}},
	}}
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	rec := func(element, event string, ms int) heatmapRecord {
		return heatmapRecord{ElementId: element, Event: event, CreatedAt: at(ms)}
	}

	builder := newHeatmapBuilder(process)
	// дошёл до конца: method 30 мс, service 1000 мс
	builder.addInstance([]heatmapRecord{
		rec("start", HistoryCompleted, 0),
		rec("f1", HistoryCompleted, 1),
		rec("method", HistoryCompleted, 31),
		rec("f2", HistoryCompleted, 31),
		rec("service", HistoryActivated, 32),
		rec("service", HistoryCompleted, 1032),
		rec("f3", HistoryCompleted, 1032),
		rec("end", HistoryCompleted, 1033),
	})
	// service прерван граничным таймером через 3000 мс
	builder.addInstance([]heatmapRecord{
		rec("start", HistoryCompleted, 0),
		rec("f1", HistoryCompleted, 0),
		rec("method", HistoryCompleted, 10),
		rec("f2", HistoryCompleted, 10),
		rec("service", HistoryActivated, 10),
		rec("service", HistoryTerminated, 3010),
		rec("boundary", HistoryCompleted, 3011),
		rec("f4", HistoryCompleted, 3011),
		rec("end", HistoryCompleted, 3011),
	})
	// ещё ждёт service
	builder.addInstance([]heatmapRecord{
		rec("start", HistoryCompleted, 0),
		rec("f1", HistoryCompleted, 0),
		rec("method", HistoryCompleted, 20),
		rec("f2", HistoryCompleted, 20),
		rec("service", HistoryActivated, 20),
	})
	// экземпляр подпроцесса: старт без предшественника - 0
	builder.addInstance([]heatmapRecord{
		rec("subStart", HistoryCompleted, 0),
		rec("f5", HistoryCompleted, 5),
	})

	elements := make(map[string]ElementTiming)
	for _, element := range builder.result() {
		elements[element.ElementId] = element
	}
	check := func(id string, count, active int, avg, minMs, maxMs float64) {
		t.Helper()
		got := elements[id]
		if got.Count != count || got.Active != active || got.AvgMs != avg || got.MinMs != minMs || got.MaxMs != maxMs {
			t.Errorf("%s = %+v, want count %d active %d avg %v min %v max %v", id, got, count, active, avg, minMs, maxMs)
		}
	}
	check("method", 3, 0, 20, 10, 30)
	check("service", 2, 1, 2000, 1000, 3000)
	check("boundary", 1, 0, 1, 1, 1)
	check("end", 2, 0, 0.5, 0, 1)
	check("start", 3, 0, 0, 0, 0)
	check("subStart", 1, 0, 0, 0, 0)
	check("f5", 1, 0, 5, 5, 5)

	if first := builder.result()[0]; first.ElementId != "service" {
		t.Errorf("first element = %s, want service: the longest by total time", first.ElementId)
	}
}
