package bpm

import "time"

// BoundaryType - вид граничного события задачи
type BoundaryType int8

const (
	// BoundaryTypeTimer - таймер: задача не завершилась за Boundary.Timer
	BoundaryTypeTimer BoundaryType = 0
	// BoundaryTypeError - ошибка: задача вернула Error с кодом Boundary.ErrorCode
	BoundaryTypeError BoundaryType = 1
)

var boundaryTypeNames = map[BoundaryType]string{
	BoundaryTypeTimer: "Timer",
	BoundaryTypeError: "Error",
}

// String - имя без префикса: BoundaryTypeTimer -> "Timer"
func (t BoundaryType) String() string {
	return boundaryTypeNames[t]
}

// Boundary - прерывающее граничное событие задачи: когда оно срабатывает, задача прерывается,
// а токен идёт по исходящим flow события ElementId
type Boundary struct {
	// ElementId - граничное событие, его Expression лежит в процессе рядом с задачей
	ElementId string
	Type      BoundaryType
	// Timer - для BoundaryTypeTimer: сколько ждать завершения задачи
	Timer time.Duration
	// ErrorCode - для BoundaryTypeError: код ошибки (errorCode у bpmn:error), пусто - любая ошибка
	ErrorCode string
}
