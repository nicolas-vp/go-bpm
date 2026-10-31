package bpmn

import "encoding/xml"

// UserTask - задача пользователя. Назначение - атрибутами Camunda 7 (camunda:assignee, camunda:candidateUsers,
// camunda:candidateGroups, camunda:formKey) или в extensionElements Zeebe (zeebe:assignmentDefinition, zeebe:formDefinition).
// Атрибуты без пространства имён в теге совпадают с атрибутом из любого пространства, поэтому camunda: не указан
type UserTask struct {
	XMLName              xml.Name            `xml:"userTask"`
	Name                 string              `xml:"name,attr"`
	Id                   string              `xml:"id,attr"`
	Default              string              `xml:"default,attr"`
	Assignee             string              `xml:"assignee,attr"`
	CandidateUsers       string              `xml:"candidateUsers,attr"`
	CandidateGroups      string              `xml:"candidateGroups,attr"`
	FormKey              string              `xml:"formKey,attr"`
	IncomingList         []Incoming          `xml:"incoming"`
	OutgoingList         []Outgoing          `xml:"outgoing"`
	ExtensionElementList []ExtensionElements `xml:"extensionElements"`
	// MultiInstance - последовательный цикл задачи, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла standardLoopCharacteristics
	Other []Element `xml:",any"`
}

// AssignmentDefinition - gobpm:assignmentDefinition или zeebe:assignmentDefinition задачи пользователя
type AssignmentDefinition struct {
	XMLName         xml.Name `xml:"assignmentDefinition"`
	Assignee        string   `xml:"assignee,attr"`
	CandidateUsers  string   `xml:"candidateUsers,attr"`
	CandidateGroups string   `xml:"candidateGroups,attr"`
}

func (d AssignmentDefinition) xmlName() xml.Name { return d.XMLName }

// FormDefinition - gobpm:formDefinition или zeebe:formDefinition: встроенная форма (formKey) или связанная (formId)
type FormDefinition struct {
	XMLName xml.Name `xml:"formDefinition"`
	FormKey string   `xml:"formKey,attr"`
	FormId  string   `xml:"formId,attr"`
}

func (d FormDefinition) xmlName() xml.Name { return d.XMLName }

// GetAssignment возвращает назначение задачи: атрибуты Camunda 7, а если их нет - gobpm:assignmentDefinition
// или zeebe:assignmentDefinition
func (task *UserTask) GetAssignment() AssignmentDefinition {
	assignment := AssignmentDefinition{Assignee: task.Assignee, CandidateUsers: task.CandidateUsers, CandidateGroups: task.CandidateGroups}
	if assignment != (AssignmentDefinition{}) {
		return assignment
	}
	definition, _ := pickExtension(collectExtensions(task.ExtensionElementList, func(e ExtensionElements) []AssignmentDefinition { return e.AssignmentDefinitions }))
	definition.XMLName = xml.Name{}
	return definition
}

// GetFormKey возвращает camunda:formKey или ключ формы gobpm:formDefinition, zeebe:formDefinition
func (task *UserTask) GetFormKey() string {
	if task.FormKey != "" {
		return task.FormKey
	}
	form, _ := pickExtension(collectExtensions(task.ExtensionElementList, func(e ExtensionElements) []FormDefinition { return e.FormDefinitions }))
	if form.FormKey != "" {
		return form.FormKey
	}
	return form.FormId
}
