package bpmn

import "encoding/xml"

type ServiceTask struct {
	XMLName              xml.Name            `xml:"serviceTask"`
	Name                 string              `xml:"name,attr"`
	Id                   string              `xml:"id,attr"`
	Default              string              `xml:"default,attr"`
	IncomingList         []Incoming          `xml:"incoming"`
	OutgoingList         []Outgoing          `xml:"outgoing"`
	ExtensionElementList []ExtensionElements `xml:"extensionElements"`
	// MultiInstance - последовательный цикл задачи, nil - без цикла
	MultiInstance *MultiInstance `xml:"multiInstanceLoopCharacteristics"`
	// Other - остальные дочерние элементы: по ним находится маркер цикла standardLoopCharacteristics
	Other []Element `xml:",any"`
}

// GetTaskDefinition возвращает gobpm:taskDefinition задачи, без него - zeebe:taskDefinition; false - не задан
func (task *ServiceTask) GetTaskDefinition() (TaskDefinition, bool) {
	return pickExtension(collectExtensions(task.ExtensionElementList, func(e ExtensionElements) []TaskDefinition { return e.TaskDefinitions }))
}

// GetType возвращает jobType задачи; пусто, если taskDefinition не задан
func (task *ServiceTask) GetType() string {
	definition, _ := task.GetTaskDefinition()
	return definition.Type
}

// GetRetries возвращает число попыток из taskDefinition, без taskDefinition - 1. 0 (retries не задан) движок
// считает одной попыткой
func (task *ServiceTask) GetRetries() int {
	if definition, ok := task.GetTaskDefinition(); ok {
		return definition.Retries
	}
	return 1
}

// GetAiTask возвращает gobpm:aiTask задачи: с ним service task - AI task, её выполняет движок запросом к модели.
// Метка есть только в пространстве имён go-bpm
func (task *ServiceTask) GetAiTask() (AiTask, bool) {
	for _, list := range task.ExtensionElementList {
		for _, ai := range list.AiTasks {
			if extensionRank(ai.XMLName) == 2 {
				return ai, true
			}
		}
	}
	return AiTask{}, false
}

// AiTask - gobpm:aiTask: настройки запроса к модели. Prompt и SystemPrompt - текст или =переменная,
// ResultVariable - куда записать ответ
type AiTask struct {
	XMLName        xml.Name `xml:"aiTask"`
	Model          string   `xml:"model,attr"`
	SystemPrompt   string   `xml:"systemPrompt,attr"`
	Prompt         string   `xml:"prompt,attr"`
	ResultVariable string   `xml:"resultVariable,attr"`
}
