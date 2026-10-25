package bpmn

import (
	"encoding/xml"
)

type Definitions struct {
	XMLName   xml.Name  `xml:"definitions"`
	Processes []Process `xml:"process"`
	Errors    []Error   `xml:"error"`
	// Path - путь файла относительно папки process через "/", по нему файл вшивается в process/bpmn-gen.go
	Path string `xml:"-"`
}

// ErrorCodes возвращает коды ошибок по id bpmn:error
func (d *Definitions) ErrorCodes() map[string]string {
	codes := make(map[string]string, len(d.Errors))
	for _, e := range d.Errors {
		codes[e.Id] = e.ErrorCode
	}
	return codes
}
