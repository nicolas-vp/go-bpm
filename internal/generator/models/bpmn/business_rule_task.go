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
	// MultiInstance - последовательный цикл задачи, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла standardLoopCharacteristics
	Other []Element `xml:",any"`
}

// CalledDecision - решение, которое вызывает задача бизнес-правила
type CalledDecision struct {
	XMLName        xml.Name `xml:"calledDecision"`
	DecisionId     string   `xml:"decisionId,attr"`
	ResultVariable string   `xml:"resultVariable,attr"`
}

func (d CalledDecision) xmlName() xml.Name { return d.XMLName }

// GetCalledDecision возвращает gobpm:calledDecision задачи, без него - zeebe:calledDecision; пустой, если не задан
func (task *BusinessRuleTask) GetCalledDecision() CalledDecision {
	decision, _ := pickExtension(collectExtensions(task.ExtensionElementList, func(e ExtensionElements) []CalledDecision { return e.CalledDecisions }))
	return decision
}

// HasTaskDefinition - задача реализована обработчиком (gobpm:taskDefinition, zeebe:taskDefinition), а не решением DMN
func (task *BusinessRuleTask) HasTaskDefinition() bool {
	_, ok := pickExtension(collectExtensions(task.ExtensionElementList, func(e ExtensionElements) []TaskDefinition { return e.TaskDefinitions }))
	return ok
}
