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
	// MultiInstance - последовательный цикл задачи, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла standardLoopCharacteristics
	Other []Element `xml:",any"`
}

// GetScript возвращает gobpm:script задачи, без него - zeebe:script; пустой, если скрипт не задан
func (task *ScriptTask) GetScript() Script {
	script, _ := pickExtension(collectExtensions(task.ExtensionElementList, func(e ExtensionElements) []Script { return e.Scripts }))
	return script
}
