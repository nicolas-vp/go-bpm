package bpmn

import "encoding/xml"

type ExtensionElements struct {
	XMLName         xml.Name         `xml:"extensionElements"`
	TaskDefinitions []TaskDefinition `xml:"taskDefinition"`
	CalledElements  []CalledElement  `xml:"calledElement"`
	Scripts         []Script         `xml:"script"`
	CalledDecisions []CalledDecision `xml:"calledDecision"`
	// AssignmentDefinitions, FormDefinitions - назначение и форма задачи пользователя в Zeebe
	AssignmentDefinitions []AssignmentDefinition `xml:"assignmentDefinition"`
	FormDefinitions       []FormDefinition       `xml:"formDefinition"`
}
