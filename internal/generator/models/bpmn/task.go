package bpmn

// Task - обычная задача (bpmn:task), для неё генерируется заглушка метода
type Task struct {
	Name         string     `xml:"name,attr"`
	Id           string     `xml:"id,attr"`
	Default      string     `xml:"default,attr"`
	IncomingList []Incoming `xml:"incoming"`
	OutgoingList []Outgoing `xml:"outgoing"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла (multi-instance)
	Other []Element `xml:",any"`
}
