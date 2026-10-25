package engine

import (
	"errors"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
)

func TestCatalog(t *testing.T) {
	s, _ := newTestServiceFromProcesses(t, generatedProcesses, bpmgen.Queues(&services.Services{}))
	list := s.Processes()
	if len(list) != len(generatedProcesses) {
		t.Fatalf("processes = %+v, want %d", list, len(generatedProcesses))
	}
	for i, definition := range list {
		if definition.BPMN != "" || (i > 0 && list[i-1].Id >= definition.Id) {
			t.Errorf("list must be sorted by id and without BPMN: %+v", list)
		}
	}
	process, err := s.Process("TestUserTask")
	if err != nil || process.Name != "Согласование заявки" || process.BPMN == "" {
		t.Errorf("TestUserTask = %+v, err %v", process, err)
	}
	if _, err := s.Process("missing"); !errors.Is(err, ErrProcessNotFound) {
		t.Errorf("missing process: err = %v", err)
	}
	if _, err := catalog(map[string]bpm.Process{"bad": {BPMN: "<definitions"}}); err == nil {
		t.Error("broken BPMN must be an error")
	}
}
