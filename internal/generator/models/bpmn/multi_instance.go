package bpmn

// MultiInstance - multiInstanceLoopCharacteristics задачи или подпроцесса. Поддерживается последовательный цикл
// с числом выполнений (loopCardinality, Camunda 7) и условием завершения (completionCondition)
type MultiInstance struct {
	IsSequential        bool   `xml:"isSequential,attr"`
	LoopCardinality     string `xml:"loopCardinality"`
	CompletionCondition string `xml:"completionCondition"`
	// LoopCharacteristics - zeebe:loopCharacteristics: цикл по коллекции, не поддерживается
	LoopCharacteristics []LoopCharacteristics `xml:"extensionElements>loopCharacteristics"`
	// Collection - camunda:collection, цикл по коллекции Camunda 7, не поддерживается
	Collection string `xml:"collection,attr"`
}

// LoopCharacteristics - zeebe:loopCharacteristics
type LoopCharacteristics struct {
	InputCollection string `xml:"inputCollection,attr"`
}
