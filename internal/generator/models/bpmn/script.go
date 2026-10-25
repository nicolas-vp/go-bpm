package bpmn

import "encoding/xml"

type Script struct {
	XMLName        xml.Name `xml:"script"`
	Expression     string   `xml:"expression,attr"`
	ResultVariable string   `xml:"resultVariable,attr"`
}
