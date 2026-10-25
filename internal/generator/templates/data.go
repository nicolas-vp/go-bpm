package templates

// DataModelTemplate - модель данных процессов; генерируется один раз пустой, дальше файл правится руками
const DataModelTemplate = `// Package model - модель данных процессов
package model

// DataModel - переменные процессов. С ней работают методы и скрипты задач, обработчики jobType,
// условия шлюзов и решения DMN: любая переменная, которую они используют, должна быть полем модели.
// Поле получает переменную процесса по тегу json, например:
//
//	Amount float64 ` + "`json:\"amount\"`" + `
//
// Вложенные структуры дают пути: client.account.number в BPMN и DMN - поле data.Client.Account.Number.
// После метода поля модели записываются поверх переменных процесса. Поле с omitempty
// в нулевом значении не меняет переменную
type DataModel struct {
}
`

// ServicesTemplate - внешние сервисы; генерируется один раз пустым, дальше файл правится руками
const ServicesTemplate = `// Package services - внешние сервисы для методов task и обработчиков jobType
package services

// Services - внешние сервисы, которые получают методы task и обработчики jobType.
// Заполняются при старте и передаются в сгенерированные Processes и Queues.
// Поля удобнее делать интерфейсами, чтобы в тестах подставлять заглушки
type Services struct {
}
`
