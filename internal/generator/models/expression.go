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
	// Boundaries - граничные события задачи
	Boundaries []bpm.Boundary
	// UserTask - назначение задачи пользователя
	UserTask *bpm.UserTask
}
