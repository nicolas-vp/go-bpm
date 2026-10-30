package bpmn

import "encoding/xml"

type CallActivity struct {
	XMLName              xml.Name            `xml:"callActivity"`
	Name                 string              `xml:"name,attr"`
	Id                   string              `xml:"id,attr"`
	ExtensionElementList []ExtensionElements `xml:"extensionElements"`
	IncomingList         []Incoming          `xml:"incoming"`
	OutgoingList         []Outgoing          `xml:"outgoing"`
	// MultiInstance - последовательный цикл задачи, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла standardLoopCharacteristics
	Other []Element `xml:",any"`
}
