package bpm

// ActivateType - что движок делает, когда токен приходит в элемент
type ActivateType int8

const (
	ActivateTypeFlow            ActivateType = 0
	ActivateTypeQueue           ActivateType = 1
	ActivateTypeSyncQueue       ActivateType = 2
	ActivateTypeStartSubprocess ActivateType = 3
	// ActivateTypeStartNewProcess - запустить отдельный экземпляр процесса ProcessId и сразу идти дальше
	ActivateTypeStartNewProcess ActivateType = 4
	ActivateTypeEnd             ActivateType = 5
	ActivateTypeMethod          ActivateType = 6
	// ActivateTypeCallActivity - запустить отдельный экземпляр процесса ProcessId и ждать его завершения
	ActivateTypeCallActivity ActivateType = 7
	// ActivateTypeTimer - промежуточный таймер: поставить отложенную на Expression.Timer задачу и ждать её
	ActivateTypeTimer ActivateType = 8
	// ActivateTypeBoundary - граничное событие задачи. Токен в него не приходит по sequence flow:
	// движок завершает его, когда сработал таймер или задача вернула ошибку (Expression.Boundaries задачи)
	ActivateTypeBoundary ActivateType = 9
)

var activateTypeNames = map[ActivateType]string{
	ActivateTypeFlow:            "Flow",
	ActivateTypeQueue:           "Queue",
	ActivateTypeSyncQueue:       "SyncQueue",
	ActivateTypeStartSubprocess: "StartSubprocess",
	ActivateTypeStartNewProcess: "StartNewProcess",
	ActivateTypeEnd:             "End",
	ActivateTypeMethod:          "Method",
	ActivateTypeCallActivity:    "CallActivity",
	ActivateTypeTimer:           "Timer",
	ActivateTypeBoundary:        "Boundary",
}

// String - имя без префикса: ActivateTypeQueue -> "Queue"
func (t ActivateType) String() string {
	return activateTypeNames[t]
}
