package bpm

import "context"

// Job - задача service task, которую движок передаёт обработчику её jobType
type Job struct {
	// InstanceUid - экземпляр процесса. Вместе с ElementId - ключ идемпотентности:
	// обработчик может быть вызван повторно, если предыдущая попытка упала
	InstanceUid string
	ProcessName string
	// BusinessKey - бизнес-ключ экземпляра из старта процесса, пусто, если его не задали
	BusinessKey string
	// ElementId - service task в процессе, которая создала задачу
	ElementId string
	// JobType - zeebe:taskDefinition type
	JobType string
	Headers map[string]any
	// LastAttempt - последняя попытка, после ошибки повторов больше не будет
	LastAttempt bool
}

// HandlerFunc - обработчик jobType. Выполняется асинхронно из очереди задач движка,
// при ошибке повторяется до Queue.Retries раз. Возвращённые переменные идут в процесс дальше
type HandlerFunc func(ctx context.Context, job Job, variables map[string]any) (map[string]any, error)
