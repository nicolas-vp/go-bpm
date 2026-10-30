package codegen

import (
	"encoding/xml"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

const eventsDefinitions = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <bpmn:process id="Events">
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:intermediateCatchEvent id="wait">
      <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing>
      <bpmn:timerEventDefinition><bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT3S</bpmn:timeDuration></bpmn:timerEventDefinition>
    </bpmn:intermediateCatchEvent>
    <bpmn:serviceTask id="charge">
      <bpmn:extensionElements><zeebe:taskDefinition type="charge" /></bpmn:extensionElements>
      <bpmn:incoming>f2</bpmn:incoming><bpmn:outgoing>f3</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:boundaryEvent id="timeout" attachedToRef="charge">
      <bpmn:outgoing>f4</bpmn:outgoing>
      <bpmn:timerEventDefinition><bpmn:timeDuration>PT1M30S</bpmn:timeDuration></bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:boundaryEvent id="declined" attachedToRef="charge">
      <bpmn:outgoing>f5</bpmn:outgoing>
      <bpmn:errorEventDefinition errorRef="Error_declined" />
    </bpmn:boundaryEvent>
    <bpmn:boundaryEvent id="anyError" attachedToRef="charge">
      <bpmn:outgoing>f6</bpmn:outgoing>
      <bpmn:errorEventDefinition />
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="wait" />
    <bpmn:sequenceFlow id="f2" sourceRef="wait" targetRef="charge" />
    <bpmn:sequenceFlow id="f3" sourceRef="charge" targetRef="end" />
    <bpmn:sequenceFlow id="f4" sourceRef="timeout" targetRef="end" />
    <bpmn:sequenceFlow id="f5" sourceRef="declined" targetRef="end" />
    <bpmn:sequenceFlow id="f6" sourceRef="anyError" targetRef="end" />
  </bpmn:process>
  <bpmn:error id="Error_declined" name="Declined" errorCode="DECLINED" />
</bpmn:definitions>`

func parseEvents(t *testing.T, source string) (map[string]*model.Expression, error) {
	t.Helper()
	var definitions bpmn.Definitions
	if err := xml.Unmarshal([]byte(source), &definitions); err != nil {
		t.Fatal(err)
	}
	var queues []model.Queue
	return ParseFromProcess(&definitions.Processes[0], &queues, &definitions)
}

func TestTimerAndBoundaryEvents(t *testing.T) {
	expressions, err := parseEvents(t, eventsDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	wait := expressions["wait"]
	if wait.ActivateType != bpm.ActivateTypeTimer || wait.Timer != 3*time.Second || !slices.Equal(wait.NextElements, []string{"f2"}) {
		t.Errorf("wait = %+v, want Timer 3s to f2", wait)
	}
	want := []bpm.Boundary{
		{ElementId: "anyError", Type: bpm.BoundaryTypeError},
		{ElementId: "declined", Type: bpm.BoundaryTypeError, ErrorCode: "DECLINED"},
		{ElementId: "timeout", Type: bpm.BoundaryTypeTimer, Timer: 90 * time.Second},
	}
	if got := expressions["charge"].Boundaries; !slices.Equal(got, want) {
		t.Errorf("charge boundaries = %+v, want %+v", got, want)
	}
	// по граничному событию токен уходит дальше, как по обычному элементу
	if timeout := expressions["timeout"]; timeout.ActivateType != bpm.ActivateTypeBoundary || !slices.Equal(timeout.NextElements, []string{"f4"}) {
		t.Errorf("timeout = %+v, want Boundary to f4", timeout)
	}
}

func TestUnsupportedEvents(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, wantErr string }{
		"go duration": {"PT1M30S", "90s", "ISO 8601"},
		"time cycle":  {"<bpmn:timeDuration>PT1M30S</bpmn:timeDuration>", "<bpmn:timeCycle>R3/PT10S</bpmn:timeCycle>", "timeDuration"},
		"non-interrupting": {`attachedToRef="charge">
      <bpmn:outgoing>f4`, `attachedToRef="charge" cancelActivity="false">
      <bpmn:outgoing>f4`, "непрерывающие"},
		"unknown error":  {`errorRef="Error_declined"`, `errorRef="Error_unknown"`, "Error_unknown"},
		"message event":  {"<bpmn:errorEventDefinition />", "<bpmn:messageEventDefinition />", "messageEventDefinition"},
		"on start event": {`id="anyError" attachedToRef="charge"`, `id="anyError" attachedToRef="start"`, "только у task"},
	} {
		source := strings.Replace(eventsDefinitions, tc.from, tc.to, 1)
		if source == eventsDefinitions {
			t.Fatalf("%s: %q not found in BPMN", name, tc.from)
		}
		if _, err := parseEvents(t, source); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
		}
	}
}

func TestTimerDate(t *testing.T) {
	for name, tc := range map[string]struct{ timer, want, wantErr string }{
		"date":          {"<bpmn:timeDate>2026-12-01T09:00:00+03:00</bpmn:timeDate>", "2026-12-01T09:00:00+03:00", ""},
		"zeebe var":     {"<bpmn:timeDate>=order.dueDate</bpmn:timeDate>", "${order.dueDate}", ""},
		"camunda var":   {"<bpmn:timeDate>${dueDate}</bpmn:timeDate>", "${dueDate}", ""},
		"no zone":       {"<bpmn:timeDate>2026-12-01T09:00:00</bpmn:timeDate>", "", "RFC 3339"},
		"expression":    {"<bpmn:timeDate>=now() + duration(\"P1D\")</bpmn:timeDate>", "", "только дата RFC 3339 или переменная"},
		"date and span": {"<bpmn:timeDate>2026-12-01T09:00:00Z</bpmn:timeDate><bpmn:timeDuration>PT1M</bpmn:timeDuration>", "", "без цикла"},
	} {
		// и промежуточный таймер, и граничный
		source := strings.Replace(eventsDefinitions, `<bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT3S</bpmn:timeDuration>`, tc.timer, 1)
		source = strings.Replace(source, "<bpmn:timeDuration>PT1M30S</bpmn:timeDuration>", tc.timer, 1)
		expressions, err := parseEvents(t, source)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if wait := expressions["wait"]; wait.TimerDate != tc.want || wait.Timer != 0 {
			t.Errorf("%s: wait = %+v, want date %q", name, wait, tc.want)
		}
		timers := (&bpm.Expression{Boundaries: expressions["charge"].Boundaries}).Timers()
		if len(timers) != 1 || timers[0].TimerDate != tc.want || timers[0].Timer != 0 {
			t.Errorf("%s: boundary timers = %+v, want date %q", name, timers, tc.want)
		}
	}
}

const userTaskTimerDefinitions = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Escalation">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approve" />
    <bpmn:boundaryEvent id="overdue" attachedToRef="approve">
      <bpmn:timerEventDefinition><bpmn:timeDuration>P3D</bpmn:timeDuration></bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="end" />
    <bpmn:endEvent id="escalated" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="approve" />
    <bpmn:sequenceFlow id="f2" sourceRef="approve" targetRef="end" />
    <bpmn:sequenceFlow id="f3" sourceRef="overdue" targetRef="escalated" />
  </bpmn:process>
</bpmn:definitions>`

func TestUserTaskBoundaryTimer(t *testing.T) {
	expressions, err := parseEvents(t, userTaskTimerDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	want := []bpm.Boundary{{ElementId: "overdue", Type: bpm.BoundaryTypeTimer, Timer: 72 * time.Hour}}
	if got := expressions["approve"].Boundaries; !slices.Equal(got, want) {
		t.Errorf("approve boundaries = %+v, want %+v", got, want)
	}
	source := strings.Replace(userTaskTimerDefinitions, "<bpmn:timerEventDefinition><bpmn:timeDuration>P3D</bpmn:timeDuration></bpmn:timerEventDefinition>",
		"<bpmn:errorEventDefinition />", 1)
	if _, err := parseEvents(t, source); err == nil || !strings.Contains(err.Error(), "у userTask approve поддерживается только граничный таймер") {
		t.Errorf("error boundary on user task: err = %v", err)
	}
}
