package bpmn

import "encoding/xml"

// IntermediateCatchEvent - промежуточное событие ожидания: таймер или сообщение
type IntermediateCatchEvent struct {
	XMLName               xml.Name               `xml:"intermediateCatchEvent"`
	Name                  string                 `xml:"name,attr"`
	Id                    string                 `xml:"id,attr"`
	IncomingList          []Incoming             `xml:"incoming"`
	OutgoingList          []Outgoing             `xml:"outgoing"`
	TimerEventDefinitions []TimerEventDefinition `xml:"timerEventDefinition"`
	// MessageEventDefinitions - ожидание сообщения
	MessageEventDefinitions []MessageEventDefinition `xml:"messageEventDefinition"`
	OtherDefinitions        []EventDefinition        `xml:",any"`
}
