package bpmn

import "encoding/xml"

// ReceiveTask - задача ожидания сообщения: токен ждёт сообщение MessageRef
type ReceiveTask struct {
	XMLName      xml.Name   `xml:"receiveTask"`
	Name         string     `xml:"name,attr"`
	Id           string     `xml:"id,attr"`
	Default      string     `xml:"default,attr"`
	MessageRef   string     `xml:"messageRef,attr"`
	IncomingList []Incoming `xml:"incoming"`
	OutgoingList []Outgoing `xml:"outgoing"`
	// MultiInstance - последовательный цикл задачи, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла standardLoopCharacteristics
	Other []Element `xml:",any"`
}
