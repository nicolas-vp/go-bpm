package bpmn

import "encoding/xml"

type TaskDefinition struct {
	XMLName xml.Name `xml:"taskDefinition"`
	Type    string   `xml:"type,attr"`
	Retries int      `xml:"retries,attr"`
}
