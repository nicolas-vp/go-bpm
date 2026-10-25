package bpmn

import "encoding/xml"

type ConditionExpression struct {
	XMLName   xml.Name `xml:"conditionExpression"`
	Type      string   `xml:"type,attr"`
	Condition string   `xml:",innerxml"`
}
