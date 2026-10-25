package bpmn

import "encoding/xml"

type ExclusiveGateway struct {
	XMLName      xml.Name   `xml:"exclusiveGateway"`
	Name         string     `xml:"name,attr"`
	Id           string     `xml:"id,attr"`
	Default      string     `xml:"default,attr"`
	IncomingList []Incoming `xml:"incoming"`
	OutgoingList []Outgoing `xml:"outgoing"`
}
