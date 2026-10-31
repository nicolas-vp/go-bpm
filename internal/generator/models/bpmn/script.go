package bpmn

import "encoding/xml"

// Script - gobpm:script или zeebe:script скриптовой задачи
type Script struct {
	XMLName        xml.Name `xml:"script"`
	Expression     string   `xml:"expression,attr"`
	ResultVariable string   `xml:"resultVariable,attr"`
}

func (s Script) xmlName() xml.Name { return s.XMLName }
