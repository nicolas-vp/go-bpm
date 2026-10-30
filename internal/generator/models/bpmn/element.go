package bpmn

import "encoding/xml"

// Element - дочерний элемент, который генератор не разбирает отдельно: по имени он либо пропускается
// (документация, лейны, аннотации), либо даёт ошибку генерации, чтобы процесс не ломался в рантайме
type Element struct {
	XMLName xml.Name
	Id      string `xml:"id,attr"`
}

// LoopMarker возвращает имя маркера цикла задачи, который не поддерживается (standard loop), если он есть.
// multiInstanceLoopCharacteristics разбирается в поле MultiInstance задачи и сюда не попадает
func LoopMarker(elements []Element) string {
	for _, element := range elements {
		switch element.XMLName.Local {
		case "standardLoopCharacteristics":
			return element.XMLName.Local
		}
	}
	return ""
}
