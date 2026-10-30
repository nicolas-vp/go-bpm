package engine

import (
	"context"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/queue"
)

// Hooks - точки подключения сервиса к движку: трассировка (OpenTelemetry), логи, свои метрики и уведомления.
// nil - хук не вызывается. Хуки вызываются синхронно, в воркере очереди: долгий хук задерживает шаг
type Hooks struct {
	// OnElement - событие элемента: activated, completed, terminated, те же, что пишет история, но независимо
	// от HistoryConfig. Вызывается в транзакции шага: если шаг откатится и повторится, событие придёт ещё раз
	OnElement func(ctx context.Context, record HistoryRecord)
	// WrapCall - обёртка вызова кода сервиса: метода task, scriptTask, businessRuleTask и обработчика service task.
	// Должна вызвать call и вернуть его ошибку, например: открыть span, вызвать call, записать ошибку и закрыть span.
	// По Call.Attempt и ошибке call видны повторы
	WrapCall func(ctx context.Context, call Call, next func(ctx context.Context) error) error
	// OnInstanceFailed - у задачи кончились попытки, экземпляр стал failed (и при выключенной History.ToDB).
	// Вызывается в транзакции, которая помечает задачу failed
	OnInstanceFailed func(ctx context.Context, failure InstanceFailure)
}

// Call - вызов кода сервиса из процесса
type Call struct {
	InstanceUid uuid.UUID
	ProcessName string
	BusinessKey string
	ElementId   string
	// TaskType - jobType service task, пусто у метода
	TaskType string
	// Attempt - номер попытки задачи очереди, с 1. Метод выполняется в шаге предыдущей задачи
	// (например, service task перед ним), и его попытки - попытки этой задачи
	Attempt int
	// LastAttempt - после ошибки повторов не будет
	LastAttempt bool
}

// InstanceFailure - экземпляр упал: задача очереди исчерпала попытки
type InstanceFailure struct {
	InstanceUid uuid.UUID
	ProcessName string
	BusinessKey string
	// ElementId - где стоит токен упавшей задачи
	ElementId string
	// Kind - вид задачи очереди: системный шаг или service task
	Kind    string
	TaskId  int64
	Attempt int
	Err     error
}

type attemptKey struct{}

// withAttempt кладёт в ctx шага попытку задачи очереди: её видит Call метода, который выполняется в этом шаге
func withAttempt(ctx context.Context, task *queue.Task) context.Context {
	return context.WithValue(ctx, attemptKey{}, task)
}

// stepCallback - колбэк шага, который знает свою задачу очереди
func stepCallback(task *queue.Task, step queue.Callback) queue.Callback {
	return func(ctx context.Context, tx queue.Tx) error {
		return step(withAttempt(ctx, task), tx)
	}
}

// callService вызывает код сервиса через Hooks.WrapCall
func (s *Engine) callService(ctx context.Context, message *instanceMessage, elementId, taskType string, call func(ctx context.Context) error) error {
	if s.hooks.WrapCall == nil {
		return call(ctx)
	}
	info := Call{
		InstanceUid: message.InstanceUid,
		ProcessName: message.ProcessName,
		BusinessKey: message.BusinessKey,
		ElementId:   elementId,
		TaskType:    taskType,
	}
	if task, ok := ctx.Value(attemptKey{}).(*queue.Task); ok {
		info.Attempt, info.LastAttempt = task.Attempt, task.IsLastAttempt()
	}
	return s.hooks.WrapCall(ctx, info, call)
}

// instanceFailed вызывает Hooks.OnInstanceFailed
func (s *Engine) instanceFailed(ctx context.Context, task *queue.Task, message *instanceMessage) {
	if s.hooks.OnInstanceFailed == nil {
		return
	}
	s.hooks.OnInstanceFailed(ctx, InstanceFailure{
		InstanceUid: message.InstanceUid,
		ProcessName: message.ProcessName,
		BusinessKey: message.BusinessKey,
		ElementId:   message.CurrentPosition,
		Kind:        task.Kind,
		TaskId:      task.Id,
		Attempt:     task.Attempt,
		Err:         task.Err,
	})
}
