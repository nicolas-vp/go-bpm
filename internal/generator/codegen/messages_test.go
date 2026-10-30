package codegen

import (
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
)

const messagesDefinitions = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="Messages">
    <bpmn:startEvent id="start" />
    <bpmn:receiveTask id="waitPayment" messageRef="Message_payment" />
    <bpmn:intermediateCatchEvent id="waitShipment">
      <bpmn:messageEventDefinition messageRef="Message_shipment" />
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="waitPayment" />
    <bpmn:sequenceFlow id="f2" sourceRef="waitPayment" targetRef="waitShipment" />
    <bpmn:sequenceFlow id="f3" sourceRef="waitShipment" targetRef="end" />
  </bpmn:process>
  <bpmn:message id="Message_payment" name="paymentReceived" />
  <bpmn:message id="Message_shipment" name="shipped">
    <bpmn:extensionElements><zeebe:subscription correlationKey="=order.id" /></bpmn:extensionElements>
  </bpmn:message>
</bpmn:definitions>`

func TestMessages(t *testing.T) {
	expressions, err := parseEvents(t, messagesDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bpm.Message{
		"waitPayment":  {Name: "paymentReceived"},
		"waitShipment": {Name: "shipped", CorrelationKey: "${order.id}"},
	} {
		expression := expressions[id]
		if expression.ActivateType != bpm.ActivateTypeMessage || expression.Message == nil || *expression.Message != want {
			t.Errorf("%s = %+v, message %+v, want %+v", id, expression, expression.Message, want)
		}
	}
}

func TestUnsupportedMessages(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, wantErr string }{
		"no ref":          {`messageRef="Message_payment" `, ``, "receive task waitPayment: не задано сообщение"},
		"unknown ref":     {`messageRef="Message_payment" `, `messageRef="Message_x" `, "сообщение Message_x не найдено"},
		"name expression": {`name="paymentReceived"`, `name="=type"`, "имя-константа"},
		"key expression":  {`correlationKey="=order.id"`, `correlationKey="=order.id + &quot;x&quot;"`, "correlationKey"},
		"timer and message": {`<bpmn:messageEventDefinition messageRef="Message_shipment" />`,
			`<bpmn:messageEventDefinition messageRef="Message_shipment" /><bpmn:timerEventDefinition><bpmn:timeDuration>PT1M</bpmn:timeDuration></bpmn:timerEventDefinition>`,
			"нужно одно определение события"},
	} {
		source := strings.Replace(messagesDefinitions, tc.from, tc.to, 1)
		if source == messagesDefinitions {
			t.Fatalf("%s: %q not found in BPMN", name, tc.from)
		}
		if _, err := parseEvents(t, source); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
		}
	}
}
