package bpmn

import (
	"encoding/xml"
	"testing"
)

func TestExtensionNamespaces(t *testing.T) {
	const header = `<serviceTask id="t" xmlns:gobpm="https://github.com/nicolas-vp/go-bpm/schema/1.0"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:other="https://example.com/other"><extensionElements>`
	for name, tc := range map[string]struct {
		extensions string
		jobType    string
		retries    int
	}{
		"zeebe":                {`<zeebe:taskDefinition type="z" retries="2" />`, "z", 2},
		"gobpm":                {`<gobpm:taskDefinition type="g" retries="3" />`, "g", 3},
		"gobpm after zeebe":    {`<zeebe:taskDefinition type="z" /><gobpm:taskDefinition type="g" />`, "g", 0},
		"gobpm before zeebe":   {`<gobpm:taskDefinition type="g" /><zeebe:taskDefinition type="z" retries="5" />`, "g", 0},
		"other namespace":      {`<other:taskDefinition type="o" />`, "", 1},
		"other and zeebe":      {`<other:taskDefinition type="o" /><zeebe:taskDefinition type="z" />`, "z", 0},
		"undeclared prefix":    {`<gobpm:taskDefinition type="g" xmlns:gobpm="gobpm" />`, "g", 0},
		"no extension element": {``, "", 1},
	} {
		var task ServiceTask
		if err := xml.Unmarshal([]byte(header+tc.extensions+`</extensionElements></serviceTask>`), &task); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if task.GetType() != tc.jobType || task.GetRetries() != tc.retries {
			t.Errorf("%s: type %q retries %d, want %q %d", name, task.GetType(), task.GetRetries(), tc.jobType, tc.retries)
		}
	}
}

func TestGoBpmExtensions(t *testing.T) {
	const source = `<definitions xmlns:gobpm="https://github.com/nicolas-vp/go-bpm/schema/1.0"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><process id="p">
  <scriptTask id="s"><extensionElements>
    <zeebe:script expression="z" /><gobpm:script expression="g" resultVariable="r" />
  </extensionElements></scriptTask>
  <businessRuleTask id="b"><extensionElements><gobpm:calledDecision decisionId="d" resultVariable="r" /></extensionElements></businessRuleTask>
  <callActivity id="c"><extensionElements><gobpm:calledElement processId="child" propagateAllChildVariables="false" /></extensionElements></callActivity>
  <userTask id="u"><extensionElements>
    <zeebe:assignmentDefinition assignee="z" /><gobpm:assignmentDefinition assignee="g" candidateGroups="a,b" />
    <gobpm:formDefinition formKey="form" />
  </extensionElements></userTask>
</process>
<message id="m" name="msg"><extensionElements><zeebe:subscription correlationKey="=z" /><gobpm:subscription correlationKey="=g" /></extensionElements></message>
</definitions>`
	var definitions Definitions
	if err := xml.Unmarshal([]byte(source), &definitions); err != nil {
		t.Fatal(err)
	}
	process := definitions.Processes[0]
	if script := process.ScriptTasks[0].GetScript(); script.Expression != "g" || script.ResultVariable != "r" {
		t.Errorf("script = %+v", script)
	}
	if decision := process.BusinessRuleTasks[0].GetCalledDecision(); decision.DecisionId != "d" || process.BusinessRuleTasks[0].HasTaskDefinition() {
		t.Errorf("calledDecision = %+v", decision)
	}
	if called := process.CallActivityList[0].GetCalledElement(); called.ProcessId != "child" || called.PropagateChildVariables() {
		t.Errorf("calledElement = %+v", called)
	}
	user := process.UserTasks[0]
	if assignment := user.GetAssignment(); assignment != (AssignmentDefinition{Assignee: "g", CandidateGroups: "a,b"}) || user.GetFormKey() != "form" {
		t.Errorf("assignment = %+v, form %q", assignment, user.GetFormKey())
	}
	if key := definitions.Messages[0].CorrelationKey(); key != "=g" {
		t.Errorf("correlationKey = %q", key)
	}
}
