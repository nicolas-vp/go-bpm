package bpmn

import "encoding/xml"

// Element - дочерний элемент, который генератор не разбирает отдельно: по имени он либо пропускается
// (документация, лейны, аннотации), либо даёт ошибку генерации, чтобы процесс не ломался в рантайме
type Element struct {
	XMLName xml.Name
	Id      string `xml:"id,attr"`
}

// LoopMarker возвращает имя маркера цикла задачи (multi-instance или standard loop), если он есть
func LoopMarker(elements []Element) string {
	for _, element := range elements {
		switch element.XMLName.Local {
		case "multiInstanceLoopCharacteristics", "standardLoopCharacteristics":
			return element.XMLName.Local
		}
	}
	return ""
}
