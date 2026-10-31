package bpmn

import "encoding/xml"

// TaskDefinition - gobpm:taskDefinition или zeebe:taskDefinition: jobType и число попыток service task
type TaskDefinition struct {
	XMLName xml.Name `xml:"taskDefinition"`
	Type    string   `xml:"type,attr"`
	Retries int      `xml:"retries,attr"`
}

func (d TaskDefinition) xmlName() xml.Name { return d.XMLName }
