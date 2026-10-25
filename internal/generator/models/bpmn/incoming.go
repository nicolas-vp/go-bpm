package bpmn

import "encoding/xml"

type Incoming struct {
	XMLName xml.Name `xml:"incoming"`
	Id      string   `xml:",innerxml"`
}
