package bpmn

import "encoding/xml"

type CalledElement struct {
	XMLName   xml.Name `xml:"calledElement"`
	ProcessId string   `xml:"processId,attr"`
	// PropagateAllChildVariables - "true", "false" или пусто; в zeebe по умолчанию true
	PropagateAllChildVariables string `xml:"propagateAllChildVariables,attr"`
}

func (e CalledElement) xmlName() xml.Name { return e.XMLName }

// PropagateChildVariables - возвращать ли переменные вызванного процесса в родителя
func (e CalledElement) PropagateChildVariables() bool {
	return e.PropagateAllChildVariables != "false"
}
