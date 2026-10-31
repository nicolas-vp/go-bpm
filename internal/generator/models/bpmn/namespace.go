package bpmn

import "encoding/xml"

// Пространства имён расширений BPMN, которые читает генератор. Локальные имена элементов и атрибутов у них
// совпадают (taskDefinition@type, script@expression, ...): gobpm:* пишет моделер go-bpm, zeebe:* - Camunda Modeler
const (
	GoBpmNamespace = "https://github.com/nicolas-vp/go-bpm/schema/1.0"
	ZeebeNamespace = "http://camunda.org/schema/zeebe/1.0"
)

// extension - элемент расширения в extensionElements: по имени XML видно его пространство имён
type extension interface {
	xmlName() xml.Name
}

// extensionRank - приоритет пространства имён элемента: gobpm важнее zeebe, расширения других пространств
// (camunda:*, свои у других моделеров) генератор не читает. Префикс без объявления xmlns encoding/xml оставляет
// в Space как есть, такой файл тоже читается
func extensionRank(name xml.Name) int {
	switch name.Space {
	case GoBpmNamespace, "gobpm":
		return 2
	case ZeebeNamespace, "zeebe":
		return 1
	}
	return 0
}

// pickExtension выбирает расширение из items: первое gobpm:*, если его нет - первое zeebe:*.
// false - ни одного расширения известного пространства имён
func pickExtension[T extension](items []T) (T, bool) {
	var best T
	bestRank := 0
	for _, item := range items {
		if rank := extensionRank(item.xmlName()); rank > bestRank {
			best, bestRank = item, rank
		}
	}
	return best, bestRank > 0
}

// collectExtensions собирает расширения одного вида из всех extensionElements элемента
func collectExtensions[T any](lists []ExtensionElements, get func(ExtensionElements) []T) []T {
	var items []T
	for _, list := range lists {
		items = append(items, get(list)...)
	}
	return items
}
