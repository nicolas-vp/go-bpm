package bpmn

import "encoding/xml"

// BusinessRuleTask - задача бизнес-правила: вызывает решение DMN (zeebe:calledDecision)
type BusinessRuleTask struct {
	XMLName              xml.Name            `xml:"businessRuleTask"`
	Name                 string              `xml:"name,attr"`
	Id                   string              `xml:"id,attr"`
	Default              string              `xml:"default,attr"`
	IncomingList         []Incoming          `xml:"incoming"`
	OutgoingList         []Outgoing          `xml:"outgoing"`
	ExtensionElementList []ExtensionElements `xml:"extensionElements"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла (multi-instance)
	Other []Element `xml:",any"`
}

// CalledDecision - решение, которое вызывает задача бизнес-правила
type CalledDecision struct {
	DecisionId     string `xml:"decisionId,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
}

// GetCalledDecision возвращает zeebe:calledDecision задачи; пустой, если не задан
func (task *BusinessRuleTask) GetCalledDecision() CalledDecision {
	for _, e := range task.ExtensionElementList {
		for _, d := range e.CalledDecisions {
			return d
		}
	}
	return CalledDecision{}
}

// HasTaskDefinition - задача реализована обработчиком (zeebe:taskDefinition), а не решением DMN
func (task *BusinessRuleTask) HasTaskDefinition() bool {
	for _, e := range task.ExtensionElementList {
		if len(e.TaskDefinitions) > 0 {
			return true
		}
	}
	return false
}
