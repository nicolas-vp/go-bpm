package bpmn

import "encoding/xml"

type SequenceFlow struct {
	XMLName       xml.Name              `xml:"sequenceFlow"`
	Id            string                `xml:"id,attr"`
	SourceRef     string                `xml:"sourceRef,attr"`
	TargetRef     string                `xml:"targetRef,attr"`
	ConditionList []ConditionExpression `xml:"conditionExpression"`
}
