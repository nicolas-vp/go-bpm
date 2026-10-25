// Package bpm - контракт между генератором go-bpm-gen, сгенерированным кодом и движком engine
package bpm

import (
	"context"
	"time"
)

// ConditionFunc - сгенерированное условие: по переменным процесса возвращает id следующего элемента.
// Ошибка - переменные не переводятся в модель данных условия (см. Condition)
type ConditionFunc func(variables map[string]any) (string, error)

// MethodFunc - метод task или scriptTask: получает переменные процесса и возвращает их новое состояние
type MethodFunc func(ctx context.Context, variables map[string]any) (map[string]any, error)

// Process - сгенерированный процесс: flow процесса и его подпроцессов по id.
// Очереди service task общие для всех процессов (один jobType может быть в нескольких), поэтому они не здесь
type Process struct {
	Flows map[string]DeploymentProcess
	// BPMN - исходный BPMN-файл процесса, по нему движок рисует диаграмму с историей экземпляра
	BPMN string
}

type DeploymentProcess struct {
	StartElement string
	Expressions  map[string]*Expression
}

type Expression struct {
	ElementId            string
	ActivateType         ActivateType
	CompleteType         CompleteType
	NextElements         []string
	Condition            ConditionFunc
	Method               MethodFunc
	ProcessId            string
	TaskType             string
	ParallelSyncQuantity int
	// PropagateChildVariables - для CallActivity: после завершения вызванного процесса его переменные
	// возвращаются в родителя (zeebe propagateAllChildVariables), иначе у родителя остаются прежние
	PropagateChildVariables bool
	// Timer - для ActivateTypeTimer: через сколько после прихода токена идти дальше
	Timer time.Duration
	// Boundaries - прерывающие граничные события task, scriptTask и service task
	Boundaries []Boundary
	// UserTask - для ActivateTypeUserTask: кому назначается задача
	UserTask *UserTask
}

// UserTask - назначение задачи пользователя, как в Camunda 7. Значение вида "${name}" - переменная процесса
// (путь через точку), её значение берётся, когда токен приходит в задачу; остальные значения - как есть
type UserTask struct {
	// Name - название задачи из BPMN, для списка задач
	Name string
	// Assignee - исполнитель: задача сразу назначена ему
	Assignee string
	// CandidateUsers, CandidateGroups - кто может взять задачу в работу. Переменная может содержать строку или список строк
	CandidateUsers  []string
	CandidateGroups []string
	// FormKey - ключ формы задачи (camunda:formKey, zeebe:formDefinition), движок его только передаёт
	FormKey string
}

// Timers возвращает граничные таймеры задачи
func (e *Expression) Timers() []Boundary {
	var timers []Boundary
	for _, boundary := range e.Boundaries {
		if boundary.Type == BoundaryTypeTimer {
			timers = append(timers, boundary)
		}
	}
	return timers
}

// ErrorBoundary возвращает граничное событие ошибки с кодом code, а если такого нет - событие без кода,
// которое ловит любую ошибку. nil - ошибку задачи никто не ловит
func (e *Expression) ErrorBoundary(code string) *Boundary {
	var catchAll *Boundary
	for i, boundary := range e.Boundaries {
		if boundary.Type != BoundaryTypeError {
			continue
		}
		if boundary.ErrorCode == code && code != "" {
			return &e.Boundaries[i]
		}
		if boundary.ErrorCode == "" && catchAll == nil {
			catchAll = &e.Boundaries[i]
		}
	}
	return catchAll
}

// Queue - вид задач очереди для service task с jobType TypeID, общая для всех процессов с этим jobType
type Queue struct {
	TypeID  string
	Retries int
	// Handler - обработчик задач очереди
	Handler HandlerFunc
}
