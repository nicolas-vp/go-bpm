package bpm

import "context"

// DecisionResult - вызов решения DMN: какие правила сработали и что вернуло решение
type DecisionResult struct {
	DecisionId string
	// RuleIds - id сработавших правил таблицы в порядке таблицы; пусто, если ни одно не подошло
	RuleIds []string
	Result  any
}

// DecisionRecorder пишет историю вызова решения. Движок кладёт его в ctx метода задачи бизнес-правила
type DecisionRecorder func(ctx context.Context, result DecisionResult) error

type decisionRecorderKey struct{}

// WithDecisionRecorder возвращает ctx, в котором RecordDecision пишет через recorder
func WithDecisionRecorder(ctx context.Context, recorder DecisionRecorder) context.Context {
	return context.WithValue(ctx, decisionRecorderKey{}, recorder)
}

// RecordDecision передаёт результат решения в recorder из ctx. Его вызывает сгенерированный метод
// задачи бизнес-правила; без recorder в ctx (например, в тестах) ничего не делает
func RecordDecision(ctx context.Context, result DecisionResult) error {
	recorder, _ := ctx.Value(decisionRecorderKey{}).(DecisionRecorder)
	if recorder == nil {
		return nil
	}
	return recorder(ctx, result)
}
