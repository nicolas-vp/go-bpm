package model

import (
	"time"

	"github.com/nicolas-vp/go-bpm/bpm"
)

type Expression struct {
	ElementId            string
	ActivateType         bpm.ActivateType
	CompleteType         bpm.CompleteType
	NextElements         []string
	ProcessId            string
	TaskType             string
	ParallelSyncQuantity int
	Condition            *Condition
	// ConditionFunc - имя функции условия (не экспортируется) в пакете процесса, если переход условный
	ConditionFunc string
	// MethodFunc - имя метода task или scriptTask в пакете процесса
	MethodFunc string
	// PropagateChildVariables - для callActivity: вернуть переменные вызванного процесса в родителя
	PropagateChildVariables bool
	// Timer - для промежуточного таймера: сколько ждать
	Timer time.Duration
	// TimerDate - для промежуточного таймера вместо Timer: до какого срока ждать (bpm.Expression.TimerDate)
	TimerDate string
	// Boundaries - граничные события задачи
	Boundaries []bpm.Boundary
	// UserTask - назначение задачи пользователя
	UserTask *bpm.UserTask
	// Loop - последовательный multi-instance элемента, nil - без цикла
	Loop *Loop
	// Message - сообщение, которое ждёт receive task или промежуточное событие
	Message *bpm.Message
}

// Loop - последовательный multi-instance: число выполнений и условие завершения
type Loop struct {
	// Cardinality - loopCardinality, 0 - не задано
	Cardinality int
	// CompletionCondition - completionCondition как в процессе, без "=" FEEL
	CompletionCondition string
	// ConditionFunc - имя функции условия завершения в пакете процесса, пусто - условия нет
	ConditionFunc string
}
