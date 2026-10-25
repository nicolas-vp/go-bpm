package bpmn

import "encoding/xml"

type CallActivity struct {
	XMLName              xml.Name            `xml:"callActivity"`
	Name                 string              `xml:"name,attr"`
	Id                   string              `xml:"id,attr"`
	ExtensionElementList []ExtensionElements `xml:"extensionElements"`
	OutgoingList         []Outgoing          `xml:"outgoing"`
}
