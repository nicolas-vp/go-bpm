// Package services - внешние сервисы для методов task и обработчиков jobType
package services

// Services - внешние сервисы, которые получают методы task и обработчики jobType.
// Заполняются при старте и передаются в сгенерированные Processes и Queues.
// Поля удобнее делать интерфейсами, чтобы в тестах подставлять заглушки
type Services struct {
}
