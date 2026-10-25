package bpmn

import "encoding/xml"

type Outgoing struct {
	XMLName xml.Name `xml:"outgoing"`
	Id      string   `xml:",innerxml"`
}
