package bpm

import (
	"context"
	"encoding/json"
	"fmt"
)

// Сгенерированный код и рукописные методы работают с моделью данных процесса T, а движок хранит переменные
// процесса map'ой. Адаптеры ниже переводят переменные в T и обратно через JSON, как при сохранении процесса
// в очередь: переменная попадает в поле T по тегу json. После метода поля T записываются поверх переменных,
// а переменные, которых в T нет (например, errorCode движка), сохраняются

// Condition делает ConditionFunc из условия шлюза на модели данных процесса T:
// условие возвращает id следующего элемента
func Condition[T any](condition func(data *T) string) ConditionFunc {
	return func(variables map[string]any) (string, error) {
		data := new(T)
		if err := toStruct(variables, data); err != nil {
			return "", err
		}
		return condition(data), nil
	}
}

// LoopCondition делает LoopConditionFunc из условия завершения цикла на модели данных процесса T
func LoopCondition[T any](condition func(data *T) bool) LoopConditionFunc {
	return func(variables map[string]any) (bool, error) {
		data := new(T)
		if err := toStruct(variables, data); err != nil {
			return false, err
		}
		return condition(data), nil
	}
}

// Method делает MethodFunc из метода на модели данных процесса T и сервисах S.
// Сервисы задаются при старте и передаются методу при каждом вызове
func Method[S, T any](services S, method func(ctx context.Context, services S, data *T) error) MethodFunc {
	return func(ctx context.Context, variables map[string]any) (map[string]any, error) {
		data := new(T)
		if err := toStruct(variables, data); err != nil {
			return nil, err
		}
		if err := method(ctx, services, data); err != nil {
			return nil, err
		}
		return mergeStruct(variables, data)
	}
}

// Handler делает HandlerFunc из обработчика jobType на модели данных процесса T и сервисах S, как Method
func Handler[S, T any](services S, handler func(ctx context.Context, services S, job Job, data *T) error) HandlerFunc {
	return func(ctx context.Context, job Job, variables map[string]any) (map[string]any, error) {
		data := new(T)
		if err := toStruct(variables, data); err != nil {
			return nil, err
		}
		if err := handler(ctx, services, job, data); err != nil {
			return nil, err
		}
		return mergeStruct(variables, data)
	}
}

// Deref возвращает значение по указателю или нулевое значение для nil. Им сгенерированное решение DMN
// читает вход - поле-указатель модели, у которого проверяется null
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// toStruct заполняет data (указатель на структуру) переменными процесса
func toStruct(variables map[string]any, data any) error {
	raw, err := json.Marshal(variables)
	if err != nil {
		return fmt.Errorf("variables to %T: %w", data, err)
	}
	if err := json.Unmarshal(raw, data); err != nil {
		return fmt.Errorf("variables to %T: %w", data, err)
	}
	return nil
}

// mergeStruct возвращает копию variables, в которой поля data записаны поверх переменных с тем же именем.
// Числа становятся float64, вложенные структуры - map[string]any
func mergeStruct(variables map[string]any, data any) (map[string]any, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("%T to variables: %w", data, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("%T to variables: %w", data, err)
	}
	result := make(map[string]any, len(variables)+len(fields))
	for name, value := range variables {
		result[name] = value
	}
	for name, value := range fields {
		result[name] = value
	}
	return result, nil
}
