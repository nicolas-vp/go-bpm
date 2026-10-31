// Package model - модель данных процессов
package model

// DataModel - переменные процессов. С ней работают методы и скрипты задач, обработчики jobType,
// условия шлюзов и решения DMN: любая переменная, которую они используют, должна быть полем модели.
// Поле получает переменную процесса по тегу json, например:
//
//	Amount float64 `json:"amount"`
//
// Вложенные структуры дают пути: client.account.number в BPMN и DMN - поле data.Client.Account.Number.
// После метода поля модели записываются поверх переменных процесса. Поле с omitempty
// в нулевом значении не меняет переменную
type DataModel struct {
	// Amount - условие шлюза Gateway_1nptppd в TestBpmProcess
	Amount int `json:"amount"`

	// Входы и выходы решений Decision_1 (a, b -> c) и SubDecision (a, c -> d) в TestDecision
	A string `json:"a"`
	B string `json:"b"`
	C string `json:"c"`
	D string `json:"d"`

	// Approved - решение задачи пользователя Approve в TestUserTask, приходит из формы при завершении задачи
	Approved bool `json:"approved"`

	// Notifications - сколько уведомлений отправила задача Notify в TestMultiInstance
	Notifications int `json:"notifications"`
	// LoopCounter - номер выполнения задачи в цикле multi-instance, с 1; после цикла переменной нет.
	// omitempty: метод вне цикла не должен записать переменную нулём
	LoopCounter int `json:"loopCounter,omitempty"`

	// Request - текст обращения клиента в TestAiTask; Category - ответ AI task Classify,
	// Triage - ответ AI task Triage: поле-структура, поэтому ответ модели разбирается как JSON
	Request  string  `json:"request"`
	Category string  `json:"category"`
	Triage   *Triage `json:"triage"`
}

// Triage - срочность обращения, ответ AI task Triage в TestAiTask
type Triage struct {
	Urgent bool   `json:"urgent"`
	Reason string `json:"reason"`
}
