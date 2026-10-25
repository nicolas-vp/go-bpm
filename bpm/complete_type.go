package bpm

// CompleteType - как движок выбирает следующий элемент после завершения текущего
type CompleteType int8

const (
	// CompleteTypeStraight - во все NextElements
	CompleteTypeStraight CompleteType = 0
	// CompleteTypeScript - в элемент, который вернула Condition
	CompleteTypeScript CompleteType = 1
)

var completeTypeNames = map[CompleteType]string{
	CompleteTypeStraight: "Straight",
	CompleteTypeScript:   "Script",
}

// String - имя без префикса: CompleteTypeScript -> "Script"
func (t CompleteType) String() string {
	return completeTypeNames[t]
}
