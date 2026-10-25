package codegen

import (
	"encoding/xml"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

func TestCreateExpressionFromCallActivity(t *testing.T) {
	for _, tc := range []struct {
		attr      string
		propagate bool
	}{
		{attr: ``, propagate: true},
		{attr: `propagateAllChildVariables="true"`, propagate: true},
		{attr: `propagateAllChildVariables="false"`, propagate: false},
	} {
		source := `<callActivity id="Call" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <extensionElements><zeebe:calledElement processId="child" ` + tc.attr + ` /></extensionElements>
</callActivity>`
		var callActivity bpmn.CallActivity
		if err := xml.Unmarshal([]byte(source), &callActivity); err != nil {
			t.Fatal(err)
		}
		expression := CreateExpressionFromCallActivity(&callActivity, &bpmn.Process{})
		if expression.ActivateType != bpm.ActivateTypeCallActivity || expression.ProcessId != "child" {
			t.Errorf("%q: got %s %q, want CallActivity \"child\"", tc.attr, expression.ActivateType, expression.ProcessId)
		}
		if expression.PropagateChildVariables != tc.propagate {
			t.Errorf("%q: PropagateChildVariables = %v, want %v", tc.attr, expression.PropagateChildVariables, tc.propagate)
		}
	}
}
