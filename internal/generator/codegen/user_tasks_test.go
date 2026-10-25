package codegen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
)

func userTaskDefinitions(task string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
  <bpmn:process id="Tasks">
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    ` + task + `
    <bpmn:endEvent id="end"><bpmn:incoming>f2</bpmn:incoming></bpmn:endEvent>
  </bpmn:process>
</bpmn:definitions>`
}

func TestUserTask(t *testing.T) {
	for _, tc := range []struct {
		name, task string
		want       bpm.UserTask
	}{
		{
			name: "camunda 7",
			task: `<bpmn:userTask id="approve" name=" Согласовать " camunda:assignee="${initiator}"
                camunda:candidateUsers="alice, bob" camunda:candidateGroups="managers,#{extra.groups}" camunda:formKey="embedded:app:approve.html">
              <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:userTask>`,
			want: bpm.UserTask{Name: "Согласовать", Assignee: "${initiator}", CandidateUsers: []string{"alice", "bob"},
				CandidateGroups: []string{"managers", "${extra.groups}"}, FormKey: "embedded:app:approve.html"},
		},
		{
			name: "zeebe",
			task: `<bpmn:userTask id="approve">
              <bpmn:extensionElements>
                <zeebe:assignmentDefinition assignee="boss" candidateGroups="=groups" />
                <zeebe:formDefinition formId="approve-form" />
              </bpmn:extensionElements>
              <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:userTask>`,
			want: bpm.UserTask{Assignee: "boss", CandidateGroups: []string{"${groups}"}, FormKey: "approve-form"},
		},
		{
			name: "no assignment",
			task: `<bpmn:userTask id="approve"><bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:userTask>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expressions, err := parseEvents(t, userTaskDefinitions(tc.task))
			if err != nil {
				t.Fatal(err)
			}
			task := expressions["approve"]
			if task.ActivateType != bpm.ActivateTypeUserTask || !reflect.DeepEqual(task.NextElements, []string{"f2"}) {
				t.Errorf("approve = %+v, want UserTask to f2", task)
			}
			if task.UserTask == nil || !reflect.DeepEqual(*task.UserTask, tc.want) {
				t.Errorf("user task = %+v, want %+v", task.UserTask, tc.want)
			}
		})
	}

	for _, tc := range []struct{ task, wantErr string }{
		{task: `<bpmn:userTask id="approve" camunda:assignee="${user.name.toUpperCase()}" />`, wantErr: "assignee"},
		{task: `<bpmn:userTask id="approve"><bpmn:extensionElements>
            <zeebe:assignmentDefinition candidateGroups="=[&quot;a&quot;, &quot;b&quot;]" /></bpmn:extensionElements></bpmn:userTask>`, wantErr: "candidateGroups"},
		{task: `<bpmn:userTask id="approve" camunda:candidateUsers="alice, ${a + b}" />`, wantErr: "candidateUsers"},
	} {
		_, err := parseEvents(t, userTaskDefinitions(tc.task))
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "не поддерживается") {
			t.Errorf("%s: err = %v, want unsupported %s", tc.task, err, tc.wantErr)
		}
	}
}
