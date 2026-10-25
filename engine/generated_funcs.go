package engine

import (
	"context"
	"fmt"

	"github.com/nicolas-vp/go-bpm/bpm"
)

// runCondition вызывает сгенерированное условие элемента и возвращает id следующего элемента
func runCondition(message *instanceMessage, expression *bpm.Expression) (string, error) {
	if expression.Condition == nil {
		return "", fmt.Errorf("process %q: element %q has no condition", message.ProcessName, expression.ElementId)
	}
	next, err := expression.Condition(message.Variables)
	if err != nil {
		return "", fmt.Errorf("process %q: element %q: %w", message.ProcessName, expression.ElementId, err)
	}
	if next == "" {
		return "", fmt.Errorf("process %q: element %q: no condition matched and no default flow", message.ProcessName, expression.ElementId)
	}
	return next, nil
}

// runMethod вызывает сгенерированный метод task или scriptTask, переменные из результата попадают в процесс дальше
func runMethod(ctx context.Context, message *instanceMessage, expression *bpm.Expression) error {
	if expression.Method == nil {
		return fmt.Errorf("process %q: element %q has no method", message.ProcessName, expression.ElementId)
	}
	variables, err := expression.Method(ctx, message.Variables)
	if err != nil {
		return fmt.Errorf("process %q: element %q: %w", message.ProcessName, expression.ElementId, err)
	}
	message.Variables = variables
	return nil
}

// runHandler вызывает сгенерированный обработчик jobType для service task, переменные из результата попадают в процесс дальше
func runHandler(ctx context.Context, handler bpm.HandlerFunc, jobType string, message *instanceMessage, lastAttempt bool) error {
	if handler == nil {
		return fmt.Errorf("no handler registered for task type %q", jobType)
	}
	job := bpm.Job{
		InstanceUid: message.InstanceUid.String(),
		ProcessName: message.ProcessName,
		ElementId:   message.CurrentPosition,
		JobType:     jobType,
		Headers:     message.Headers,
		LastAttempt: lastAttempt,
	}
	variables, err := handler(ctx, job, message.Variables)
	if err != nil {
		return err
	}
	message.Variables = variables
	return nil
}
