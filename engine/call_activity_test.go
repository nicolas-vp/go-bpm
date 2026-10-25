package engine

import (
	"bytes"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
)

var historyInstance = regexp.MustCompile(`process="([^"]+)" instance=(\S+)(?: parent=(\S+))?`)

func TestCallActivity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		propagate bool
		want      map[string]any
	}{
		{name: "propagate", propagate: true, want: map[string]any{"a": "child", "child": "done"}},
		{name: "keep parent variables", propagate: false, want: map[string]any{"a": "parent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deployment := map[string]bpm.DeploymentProcess{
				"parent": {StartElement: "start", Expressions: map[string]*bpm.Expression{
					"start": {ElementId: "start", NextElements: []string{"call"}},
					"call": {ElementId: "call", ActivateType: bpm.ActivateTypeCallActivity, ProcessId: "called",
						PropagateChildVariables: tc.propagate, NextElements: []string{"after"}},
					"after": {ElementId: "after", ActivateType: bpm.ActivateTypeQueue, TaskType: "after", NextElements: []string{"end"}},
					"end":   {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
				}},
				"called": {StartElement: "cStart", Expressions: map[string]*bpm.Expression{
					"cStart": {ElementId: "cStart", NextElements: []string{"cTask"}},
					"cTask":  {ElementId: "cTask", ActivateType: bpm.ActivateTypeQueue, TaskType: "child", NextElements: []string{"cEnd"}},
					"cEnd":   {ElementId: "cEnd", ActivateType: bpm.ActivateTypeEnd},
				}},
			}
			s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "after"}, {TypeID: "child"}})
			var out bytes.Buffer
			s.history = HistoryConfig{ToLog: func() bool { return true }}
			s.historyOut = &out

			startAndActivate(t, s, q, nil, "parent", map[string]any{"a": "parent"})
			// родитель ждёт: в очереди только задача вызванного процесса
			if len(q.pushed) != 1 {
				t.Fatalf("parent must wait for the called process, got %d tasks", len(q.pushed))
			}
			child := completeTaskWith(t, s, q, nil, "child", func(message *instanceMessage) {
				if message.ProcessName != "called" {
					t.Fatalf("got process %q, want called", message.ProcessName)
				}
				// вызванный процесс видит переменные родителя, меняет их и добавляет свои
				if message.Variables["a"] != "parent" {
					t.Fatalf("called process must get parent variables, got %v", message.Variables)
				}
				message.Variables["a"] = "child"
				message.Variables["child"] = "done"
			})
			if child.ProcessName != "parent" || len(child.ParentProcess) != 0 {
				t.Fatalf("must return to parent without parents, got %q %v", child.ProcessName, child.ParentProcess)
			}
			if !maps.Equal(child.Variables, tc.want) {
				t.Errorf("parent variables = %v, want %v", child.Variables, tc.want)
			}
			completeTask(t, s, q, nil, "after")
			if len(q.pushed) != 0 {
				t.Fatalf("process must be finished, got %d tasks", len(q.pushed))
			}

			var history []string
			for _, match := range historyElement.FindAllStringSubmatch(out.String(), -1) {
				history = append(history, match[1]+" "+match[2])
			}
			want := []string{
				"completed start", "activated call", "completed cStart", "activated cTask",
				"completed cTask", "completed cEnd", "completed call",
				"activated after", "completed after", "completed end",
			}
			if !slices.Equal(history, want) {
				t.Errorf("history = %v, want %v", history, want)
			}

			// у записей вызванного процесса есть ссылка на родителя, у родителя её нет
			instances := map[string]map[string]bool{}
			for _, match := range historyInstance.FindAllStringSubmatch(out.String(), -1) {
				if instances[match[1]] == nil {
					instances[match[1]] = map[string]bool{}
				}
				instances[match[1]][match[2]+"|"+match[3]] = true
			}
			if len(instances["parent"]) != 1 || len(instances["called"]) != 1 {
				t.Fatalf("every process must have one instance and one parent, got %v", instances)
			}
			var parentInstance string
			for key := range instances["parent"] {
				parentInstance = key[:len(key)-1]
				if key[len(key)-1] != '|' {
					t.Errorf("root process must have no parent, got %q", key)
				}
			}
			// ссылка на родителя - экземпляр и элемент callActivity, который ждёт
			for key := range instances["called"] {
				if !strings.HasSuffix(key, "|"+parentInstance+"/call") {
					t.Errorf("called process must refer to parent %s/call, got %q", parentInstance, key)
				}
			}
		})
	}
}
