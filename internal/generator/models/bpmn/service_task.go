package bpmn

import "encoding/xml"

type ServiceTask struct {
	XMLName              xml.Name            `xml:"serviceTask"`
	Name                 string              `xml:"name,attr"`
	Id                   string              `xml:"id,attr"`
	Default              string              `xml:"default,attr"`
	IncomingList         []Incoming          `xml:"incoming"`
	OutgoingList         []Outgoing          `xml:"outgoing"`
	ExtensionElementList []ExtensionElements `xml:"extensionElements"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла (multi-instance)
	Other []Element `xml:",any"`
}

func (task *ServiceTask) GetType() string {
	for _, e := range task.ExtensionElementList {
		for _, d := range e.TaskDefinitions {
			return d.Type
		}
	}
	// TODO : Подумать над обработкой ошибки
	panic("type for service task is not defined")
}

func (task *ServiceTask) GetRetries() int {
	for _, e := range task.ExtensionElementList {
		for _, d := range e.TaskDefinitions {
			return d.Retries
		}
	}
	return 1
}
