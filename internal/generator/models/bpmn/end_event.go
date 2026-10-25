package bpmn

import "encoding/xml"

type EndEvent struct {
	XMLName      xml.Name   `xml:"endEvent"`
	Name         string     `xml:"name,attr"`
	Id           string     `xml:"id,attr"`
	IncomingList []Incoming `xml:"incoming"`
	// OtherDefinitions - terminate, error и прочие определения: поддерживается только обычный конец
	OtherDefinitions []EventDefinition `xml:",any"`
}
