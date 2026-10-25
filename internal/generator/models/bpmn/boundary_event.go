package bpmn

import "encoding/xml"

// BoundaryEvent - граничное событие задачи: таймер или ошибка
type BoundaryEvent struct {
	XMLName       xml.Name `xml:"boundaryEvent"`
	Name          string   `xml:"name,attr"`
	Id            string   `xml:"id,attr"`
	AttachedToRef string   `xml:"attachedToRef,attr"`
	// CancelActivity - "false" у непрерывающего события, по умолчанию событие прерывает задачу
	CancelActivity        string                 `xml:"cancelActivity,attr"`
	OutgoingList          []Outgoing             `xml:"outgoing"`
	TimerEventDefinitions []TimerEventDefinition `xml:"timerEventDefinition"`
	ErrorEventDefinitions []ErrorEventDefinition `xml:"errorEventDefinition"`
	// OtherDefinitions - message, signal и прочие определения, которые генератор не поддерживает
	OtherDefinitions []EventDefinition `xml:",any"`
}

// Interrupting - прерывает ли событие задачу
func (event *BoundaryEvent) Interrupting() bool {
	return event.CancelActivity != "false"
}
