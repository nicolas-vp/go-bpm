package bpmn

import (
	"encoding/xml"
	"strings"
)

// TimerEventDefinition - таймер события: длительность, дата или цикл
type TimerEventDefinition struct {
	XMLName      xml.Name `xml:"timerEventDefinition"`
	TimeDuration string   `xml:"timeDuration"`
	TimeDate     string   `xml:"timeDate"`
	TimeCycle    string   `xml:"timeCycle"`
}

// ErrorEventDefinition - ошибка события, ErrorRef ссылается на bpmn:error в definitions
type ErrorEventDefinition struct {
	XMLName  xml.Name `xml:"errorEventDefinition"`
	ErrorRef string   `xml:"errorRef,attr"`
}

// EventDefinition - любой дочерний элемент события, кроме разобранных отдельно
type EventDefinition struct {
	XMLName xml.Name
}

// UnsupportedDefinition возвращает имя первого определения события вида *EventDefinition, если оно есть
func UnsupportedDefinition(definitions []EventDefinition) string {
	for _, definition := range definitions {
		if strings.HasSuffix(definition.XMLName.Local, "EventDefinition") {
			return definition.XMLName.Local
		}
	}
	return ""
}
