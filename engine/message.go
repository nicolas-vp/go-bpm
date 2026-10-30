package engine

import (
	"maps"

	"github.com/google/uuid"
)

// Как токен входит в элемент: moveNext активирует элемент или завершает его
const (
	moveActivate = "Activate"
	moveComplete = "Complete"
)

// Типы системных задач (systemTask.Type)
const (
	// systemStart - старт экземпляра процесса
	systemStart = "_system.start"
	// systemTimer - сработал таймер: промежуточный или граничный
	systemTimer = "_system.timer"
	// systemUserTask - задачу пользователя завершили через API: токен идёт дальше
	systemUserTask = "_system.userTask"
	// systemLoop - следующее выполнение метода task или scriptTask с циклом: каждое выполнение - отдельный шаг
	systemLoop = "_system.loop"
)

// instanceMessage - токен экземпляра процесса: где он стоит и с какими переменными.
// Лежит в payload задач очереди, имена полей - часть формата задач в bpm_queue
type instanceMessage struct {
	InstanceUid uuid.UUID
	ProcessName string
	// BusinessKey - бизнес-ключ корневого экземпляра, задаётся при старте; дочерние экземпляры наследуют его
	BusinessKey     string `json:",omitempty"`
	CurrentPosition string
	Headers         map[string]any
	Variables       map[string]any
	ParentProcess   map[uuid.UUID]*parentProcess
	// WaitId - строка bpm_wait задачи с граничными таймерами, пока она ждёт завершения (см. bpm_wait)
	WaitId uuid.UUID `json:",omitzero"`
	// Loops - номер текущего выполнения элементов с циклом (multi-instance) по loopKey, пока цикл идёт
	Loops map[string]int `json:",omitempty"`
}

// parentProcess - родитель экземпляра подпроцесса или callActivity, в который вернётся токен
type parentProcess struct {
	InstanceUid uuid.UUID
	ProcessName string
	Element     string
	// RestoreVariables - при возврате в родителя вернуть ему Variables, а не переменные подпроцесса:
	// callActivity без propagateAllChildVariables
	RestoreVariables bool           `json:",omitempty"`
	Variables        map[string]any `json:",omitempty"`
}

func (message *instanceMessage) addParentProcess(newInstanceUid uuid.UUID, elementId string) {
	if message.ParentProcess == nil {
		message.ParentProcess = make(map[uuid.UUID]*parentProcess)
	}
	message.ParentProcess[newInstanceUid] = &parentProcess{
		InstanceUid: message.InstanceUid,
		ProcessName: message.ProcessName,
		Element:     elementId,
	}
}

// clone копирует сообщение для отдельной параллельной ветки
func (message *instanceMessage) clone() *instanceMessage {
	clone := *message
	clone.Headers = maps.Clone(message.Headers)
	clone.Variables = maps.Clone(message.Variables)
	clone.ParentProcess = maps.Clone(message.ParentProcess)
	clone.Loops = maps.Clone(message.Loops)
	return &clone
}
