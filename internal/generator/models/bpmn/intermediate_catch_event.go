package bpmn

import "encoding/xml"

// IntermediateCatchEvent - промежуточное событие ожидания, поддерживается только таймер
type IntermediateCatchEvent struct {
	XMLName               xml.Name               `xml:"intermediateCatchEvent"`
	Name                  string                 `xml:"name,attr"`
	Id                    string                 `xml:"id,attr"`
	IncomingList          []Incoming             `xml:"incoming"`
	OutgoingList          []Outgoing             `xml:"outgoing"`
	TimerEventDefinitions []TimerEventDefinition `xml:"timerEventDefinition"`
	OtherDefinitions      []EventDefinition      `xml:",any"`
}
