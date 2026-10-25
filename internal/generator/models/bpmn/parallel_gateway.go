package bpmn

import "encoding/xml"

type ParallelGateway struct {
	XMLName      xml.Name   `xml:"parallelGateway"`
	Name         string     `xml:"name,attr"`
	Id           string     `xml:"id,attr"`
	IncomingList []Incoming `xml:"incoming"`
	OutgoingList []Outgoing `xml:"outgoing"`
}
