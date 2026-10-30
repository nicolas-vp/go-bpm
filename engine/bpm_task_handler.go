package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

type systemTask struct {
	Type    string           `json:"type"`
	Message *instanceMessage `json:"message"`
	// Boundary - для Timer: граничный таймер задачи Message.CurrentPosition, пусто у промежуточного таймера
	Boundary string `json:"boundary,omitempty"`
	// Delay - для Timer: на сколько отложена задача, для отладки в bpm_queue
	Delay time.Duration `json:"delay,omitempty"`
}

func (s *Engine) handleSystemTask(ctx context.Context, task *queue.Task) error {
	var st systemTask
	if err := json.Unmarshal(task.Payload, &st); err != nil {
		return fmt.Errorf("%w: unmarshal system task: %v", queue.ErrCancel, err)
	}

	switch st.Type {
	case systemStart:
		task.OnSucceeded(stepCallback(task, func(ctx context.Context, tx queue.Tx) error {
			return s.startRoot(ctx, tx, st.Message)
		}))
		return nil
	case systemTimer:
		task.OnSucceeded(stepCallback(task, func(ctx context.Context, tx queue.Tx) error {
			return s.fireTimer(ctx, tx, st.Message, st.Boundary)
		}))
		return nil
	case systemLoop:
		task.OnSucceeded(stepCallback(task, func(ctx context.Context, tx queue.Tx) error {
			return s.moveNext(ctx, tx, st.Message, st.Message.CurrentPosition, moveActivate)
		}))
		return nil
	case systemUserTask, systemMessage:
		task.OnSucceeded(stepCallback(task, func(ctx context.Context, tx queue.Tx) error {
			return s.moveNext(ctx, tx, st.Message, st.Message.CurrentPosition, moveComplete)
		}))
		return nil
	default:
		return fmt.Errorf("%w: unknown system task type %q", queue.ErrCancel, st.Type)
	}
}

// startRoot - шаг системной задачи старта: экземпляр из StartProcess входит в стартовое событие
func (s *Engine) startRoot(ctx context.Context, tx queue.Tx, message *instanceMessage) error {
	if err := s.startInstance(ctx, tx, message); err != nil {
		return err
	}
	return s.moveNext(ctx, tx, message, message.CurrentPosition, moveActivate)
}

func (s *Engine) serviceTaskHandler(taskType string, handler bpm.HandlerFunc) queue.Handler {
	return func(ctx context.Context, task *queue.Task) error {
		var message instanceMessage
		if err := json.Unmarshal(task.Payload, &message); err != nil {
			return fmt.Errorf("%w: unmarshal message: %v", queue.ErrCancel, err)
		}
		err := s.callService(withAttempt(ctx, task), &message, message.CurrentPosition, taskType, func(ctx context.Context) error {
			return runHandler(ctx, handler, taskType, &message, task.IsLastAttempt())
		})
		if err != nil {
			step, err := s.taskErrorStep(ctx, task.IsLastAttempt(), taskType, &message, err)
			if err != nil {
				return err
			}
			task.OnSucceeded(stepCallback(task, step))
			return nil
		}

		task.OnSucceeded(stepCallback(task, func(ctx context.Context, tx queue.Tx) error {
			return s.moveNext(ctx, tx, &message, message.CurrentPosition, moveComplete)
		}))
		return nil
	}
}

// taskErrorStep решает, что делать с ошибкой обработчика service task:
//   - её ловит граничное событие ошибки - возвращается шаг для OnSucceeded, который ведёт токен по событию;
//   - бизнес-ошибку bpm.Error никто не ловит - задача падает без повторов: повтор вернёт ту же ошибку;
//   - техническая ошибка - очередь повторит задачу, а после последней попытки её ловит событие ошибки без кода.
func (s *Engine) taskErrorStep(ctx context.Context, lastAttempt bool, taskType string, message *instanceMessage, taskErr error) (queue.Callback, error) {
	expression, err := s.getExpression(message.ProcessName, message.CurrentPosition)
	if err != nil {
		return nil, errors.Join(taskErr, err)
	}
	if boundary := errorBoundary(expression, taskErr, lastAttempt); boundary != nil {
		return func(ctx context.Context, tx queue.Tx) error {
			return s.leaveByBoundary(ctx, tx, message, expression, boundary, taskErr)
		}, nil
	}
	var bpmErr *bpm.Error
	if errors.As(taskErr, &bpmErr) {
		s.log.ErrorContext(ctx, "no boundary event catches task error", "process", message.ProcessName,
			"instance", message.InstanceUid, "task_type", taskType, "element", message.CurrentPosition, "error", taskErr)
		return nil, fmt.Errorf("%w: %w", queue.ErrNonRetryable, taskErr)
	}
	if lastAttempt {
		s.log.ErrorContext(ctx, "task failed, no attempts left", "process", message.ProcessName,
			"instance", message.InstanceUid, "task_type", taskType, "element", message.CurrentPosition, "error", taskErr)
	}
	return nil, taskErr
}
