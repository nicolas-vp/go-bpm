package model

type Queue struct {
	TypeID  string
	Retries int
	// HandlerFunc - имя обработчика jobType в пакете процесса
	HandlerFunc string
}
