package bpmn

type Process struct {
	Name              string             `xml:"name,attr"`
	Id                string             `xml:"id,attr"`
	IsExecutable      bool               `xml:"isExecutable,attr"`
	StartEvents       []StartEvent       `xml:"startEvent"`
	ServiceTasks      []ServiceTask      `xml:"serviceTask"`
	EndEvents         []EndEvent         `xml:"endEvent"`
	ExclusiveGateways []ExclusiveGateway `xml:"exclusiveGateway"`
	ParallelGateways  []ParallelGateway  `xml:"parallelGateway"`
	CallActivityList  []CallActivity     `xml:"callActivity"`
	SequenceFlowList  []SequenceFlow     `xml:"sequenceFlow"`
	SubProcessList    []Process          `xml:"subProcess"`
	// IncomingList и OutgoingList - переходы встроенного подпроцесса как элемента родителя
	IncomingList      []Incoming         `xml:"incoming"`
	OutgoingList      []Outgoing         `xml:"outgoing"`
	Tasks             []Task             `xml:"task"`
	ScriptTasks       []ScriptTask       `xml:"scriptTask"`
	BusinessRuleTasks []BusinessRuleTask `xml:"businessRuleTask"`
	UserTasks         []UserTask         `xml:"userTask"`
	BoundaryEvents    []BoundaryEvent    `xml:"boundaryEvent"`
	// IntermediateCatchEvents - промежуточные события ожидания, поддерживается таймер
	IntermediateCatchEvents []IntermediateCatchEvent `xml:"intermediateCatchEvent"`
	// MultiInstance - последовательный цикл встроенного подпроцесса, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - элементы, которые генератор не поддерживает (inclusive gateway, manual task и т.п.), и служебные
	// (документация, лейны, аннотации); первые - ошибка генерации
	Other []Element `xml:",any"`
}

// GetMethodTasks возвращает задачи процесса и всех его подпроцессов, для которых генерируются методы
func (process *Process) GetMethodTasks() []Task {
	tasks := append([]Task(nil), process.Tasks...)
	for _, subProcess := range process.SubProcessList {
		tasks = append(tasks, subProcess.GetMethodTasks()...)
	}
	return tasks
}

// GetScriptTasks возвращает скриптовые задачи процесса и всех его подпроцессов
func (process *Process) GetScriptTasks() []ScriptTask {
	tasks := append([]ScriptTask(nil), process.ScriptTasks...)
	for _, subProcess := range process.SubProcessList {
		tasks = append(tasks, subProcess.GetScriptTasks()...)
	}
	return tasks
}

// GetBusinessRuleTasks возвращает задачи бизнес-правил процесса и всех его подпроцессов
func (process *Process) GetBusinessRuleTasks() []BusinessRuleTask {
	tasks := append([]BusinessRuleTask(nil), process.BusinessRuleTasks...)
	for _, subProcess := range process.SubProcessList {
		tasks = append(tasks, subProcess.GetBusinessRuleTasks()...)
	}
	return tasks
}

func (process *Process) GetStartingElement() string {
	for _, s := range process.StartEvents {
		return s.Id
	}
	panic("no starting point for process:" + process.Name)
}
