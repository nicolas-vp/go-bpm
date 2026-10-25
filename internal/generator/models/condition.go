package model

// Condition - условный переход из элемента процесса
type Condition struct {
	Branches    []ConditionBranch
	DefaultFlow string
}

// ConditionBranch - исходящий flow и условие перехода по нему
type ConditionBranch struct {
	FlowId     string
	Expression string
}
