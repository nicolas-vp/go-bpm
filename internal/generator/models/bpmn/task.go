package bpmn

// Task - обычная задача (bpmn:task), для неё генерируется заглушка метода
type Task struct {
	Name         string     `xml:"name,attr"`
	Id           string     `xml:"id,attr"`
	Default      string     `xml:"default,attr"`
	IncomingList []Incoming `xml:"incoming"`
	OutgoingList []Outgoing `xml:"outgoing"`
	// MultiInstance - последовательный цикл задачи, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла standardLoopCharacteristics
	Other []Element `xml:",any"`
}
