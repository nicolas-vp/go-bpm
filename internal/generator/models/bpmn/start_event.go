package bpmn

import "encoding/xml"

type StartEvent struct {
	XMLName      xml.Name   `xml:"startEvent"`
	Name         string     `xml:"name,attr"`
	Id           string     `xml:"id,attr"`
	Default      string     `xml:"default,attr"`
	OutgoingList []Outgoing `xml:"outgoing"`
	// OtherDefinitions - таймер, сообщение и прочие определения: поддерживается только обычный старт
	OtherDefinitions []EventDefinition `xml:",any"`
}
