package engine

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/bpm"
)

func TestDiagramFor(t *testing.T) {
	var (
		main     = uuid.New()
		sub      = uuid.New() // встроенный подпроцесс main
		called   = uuid.New() // callActivity main -> other
		self     = uuid.New() // callActivity main -> main
		selfSub  = uuid.New() // встроенный подпроцесс внутри self
		unrelate = uuid.New()
	)
	record := func(instance uuid.UUID, parent *uuid.UUID, parentElement, process, element string) HistoryRecord {
		return HistoryRecord{InstanceUid: instance, ParentInstanceUid: parent, ParentElementId: parentElement,
			ProcessName: process, ElementId: element}
	}
	records := []HistoryRecord{
		record(main, nil, "", "main", "start"),
		record(sub, &main, "sub", "main.sub", "subStart"),
		record(called, &main, "callOther", "other", "otherStart"),
		record(called, &main, "callOther", "other", "otherEnd"),
		record(self, &main, "callSelf", "main", "selfStart"),
		record(selfSub, &self, "sub", "main.sub", "selfSubStart"),
		record(main, nil, "", "main", "end"),
	}
	processes := map[string]bpm.Process{"main": {BPMN: "<main/>"}, "other": {BPMN: "<other/>"}}

	diagram, err := diagramFor(main, records, processes)
	if err != nil {
		t.Fatal(err)
	}
	var elements []string
	for _, r := range diagram.Records {
		elements = append(elements, r.ElementId)
	}
	if want := []string{"start", "subStart", "end"}; !slices.Equal(elements, want) || diagram.BPMN != "<main/>" {
		t.Errorf("main: elements = %v, bpmn = %q, want %v <main/>", elements, diagram.BPMN, want)
	}
	// дочерние экземпляры callActivity - для перехода из элемента, который их запустил
	var children []string
	for _, child := range diagram.Children {
		children = append(children, child.ProcessName+"/"+child.ElementId+"/"+child.InstanceUid.String())
	}
	if want := []string{"other/callOther/" + called.String(), "main/callSelf/" + self.String()}; !slices.Equal(children, want) || diagram.Parent != nil {
		t.Errorf("main: children = %v, parent = %+v, want %v and no parent", children, diagram.Parent, want)
	}

	// экземпляр встроенного подпроцесса рисуется на диаграмме своего процесса
	if diagram, err := diagramFor(sub, records, processes); err != nil || diagram.BPMN != "<main/>" || len(diagram.Records) != 1 {
		t.Errorf("sub: %+v, %v", diagram, err)
	}
	// у вызванного процесса ссылка на родителя и его callActivity
	if diagram, err := diagramFor(called, records, processes); err != nil || diagram.Parent == nil ||
		diagram.Parent.InstanceUid != main || diagram.Parent.ElementId != "callOther" || diagram.BPMN != "<other/>" {
		t.Errorf("called: %+v, %v", diagram, err)
	}
	if _, err := diagramFor(unrelate, records, processes); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("unknown instance: err = %v, want ErrInstanceNotFound", err)
	}
	if _, err := diagramFor(main, records, map[string]bpm.Process{}); err == nil {
		t.Error("process without BPMN must fail")
	}
}

// записи до колонки parent_element_id: элемент вызова - последний activated родителя перед стартом дочернего
func TestDiagramForWithoutParentElement(t *testing.T) {
	main, called := uuid.New(), uuid.New()
	records := []HistoryRecord{
		{InstanceUid: main, ProcessName: "main", ElementId: "task", Event: HistoryActivated},
		{InstanceUid: main, ProcessName: "main", ElementId: "task", Event: HistoryCompleted},
		{InstanceUid: main, ProcessName: "main", ElementId: "call", Event: HistoryActivated},
		{InstanceUid: called, ParentInstanceUid: &main, ProcessName: "other", ElementId: "otherStart", Event: HistoryCompleted},
		{InstanceUid: main, ProcessName: "main", ElementId: "call", Event: HistoryCompleted},
	}
	diagram, err := diagramFor(main, records, map[string]bpm.Process{"main": {BPMN: "<main/>"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagram.Children) != 1 || diagram.Children[0].ElementId != "call" || diagram.Children[0].InstanceUid != called {
		t.Errorf("children = %+v, want %s from call", diagram.Children, called)
	}
}
