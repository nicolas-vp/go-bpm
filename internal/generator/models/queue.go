package model

type Queue struct {
	TypeID  string
	Retries int
	// HandlerFunc - имя обработчика jobType в пакете handlers, пусто у AI task
	HandlerFunc string
	// AI - jobType AI task: обработчик не генерируется, задачи выполняет движок (engine.Config.AI)
	AI bool
}
