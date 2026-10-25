package bpmn

import "encoding/xml"

type ScriptTask struct {
	XMLName              xml.Name            `xml:"scriptTask"`
	Name                 string              `xml:"name,attr"`
	Id                   string              `xml:"id,attr"`
	Default              string              `xml:"default,attr"`
	IncomingList         []Incoming          `xml:"incoming"`
	OutgoingList         []Outgoing          `xml:"outgoing"`
	ExtensionElementList []ExtensionElements `xml:"extensionElements"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла (multi-instance)
	Other []Element `xml:",any"`
}

// GetScript возвращает zeebe:script задачи; пустой, если скрипт не задан
func (task *ScriptTask) GetScript() Script {
	for _, e := range task.ExtensionElementList {
		for _, s := range e.Scripts {
			return s
		}
	}
	return Script{}
}
