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

// AssignmentDefinition - zeebe:assignmentDefinition задачи пользователя
type AssignmentDefinition struct {
	Assignee        string `xml:"assignee,attr"`
	CandidateUsers  string `xml:"candidateUsers,attr"`
	CandidateGroups string `xml:"candidateGroups,attr"`
}

// FormDefinition - zeebe:formDefinition: встроенная форма (formKey) или связанная (formId)
type FormDefinition struct {
	FormKey string `xml:"formKey,attr"`
	FormId  string `xml:"formId,attr"`
}

// GetAssignment возвращает назначение задачи: атрибуты Camunda 7, а если их нет - zeebe:assignmentDefinition
func (task *UserTask) GetAssignment() AssignmentDefinition {
	assignment := AssignmentDefinition{Assignee: task.Assignee, CandidateUsers: task.CandidateUsers, CandidateGroups: task.CandidateGroups}
	if assignment != (AssignmentDefinition{}) {
		return assignment
	}
	for _, e := range task.ExtensionElementList {
		for _, d := range e.AssignmentDefinitions {
			return d
		}
	}
	return assignment
}

// GetFormKey возвращает camunda:formKey или ключ формы zeebe:formDefinition
func (task *UserTask) GetFormKey() string {
	if task.FormKey != "" {
		return task.FormKey
	}
	for _, e := range task.ExtensionElementList {
		for _, d := range e.FormDefinitions {
			if d.FormKey != "" {
				return d.FormKey
			}
			return d.FormId
		}
	}
	return ""
}
