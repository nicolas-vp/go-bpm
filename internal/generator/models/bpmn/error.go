package bpmn

import "encoding/xml"

// Error - bpmn:error из definitions, на него ссылаются события ошибки
type Error struct {
	XMLName   xml.Name `xml:"error"`
	Id        string   `xml:"id,attr"`
	Name      string   `xml:"name,attr"`
	ErrorCode string   `xml:"errorCode,attr"`
}
