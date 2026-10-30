package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// moveNext продвигает токен процесса. Все побочные эффекты (новые задачи, счётчики синхронизации)
// пишутся в tx, поэтому шаг применяется атомарно вместе с завершением текущей задачи очереди.
func (s *Engine) moveNext(ctx context.Context, tx queue.Tx, message *instanceMessage, currentPosition string, moveType string) error {
	expression, err := s.getExpression(message.ProcessName, currentPosition)
	if err != nil {
		return err
	}
	var activateType = expression.ActivateType

	if moveType == moveActivate && expression.Loop != nil {
		enterLoop(message, expression)
	}
	if moveType == moveComplete {
		switch expression.ActivateType {
		case bpm.ActivateTypeQueue:
			// задачу с граничными таймерами мог уже прервать таймер
			won, err := s.finishWait(ctx, tx, message, expression)
			if err != nil || !won {
				return err
			}
			activateType = bpm.ActivateTypeFlow
		case bpm.ActivateTypeSyncQueue, bpm.ActivateTypeStartSubprocess, bpm.ActivateTypeCallActivity, bpm.ActivateTypeTimer,
			bpm.ActivateTypeUserTask:
			activateType = bpm.ActivateTypeFlow
		}
	}

	switch activateType {
	case bpm.ActivateTypeFlow:
		return s.completeElement(ctx, tx, message, expression)

	case bpm.ActivateTypeQueue:
		message.CurrentPosition = expression.ElementId
		key, err := s.startWait(ctx, tx, message, expression)
		if err != nil {
			return err
		}
		if err := s.addMessageToQueue(ctx, tx, expression.TaskType, message, key); err != nil {
			return err
		}
		return s.logHistory(ctx, tx, HistoryActivated, message, expression)

	case bpm.ActivateTypeTimer:
		return s.activateTimer(ctx, tx, message, expression)

	case bpm.ActivateTypeUserTask:
		return s.activateUserTask(ctx, tx, message, expression)

	case bpm.ActivateTypeBoundary:
		return fmt.Errorf("process %q: boundary event %q is reached by sequence flow", message.ProcessName, expression.ElementId)

	case bpm.ActivateTypeSyncQueue:
		hash := syncKey(message.InstanceUid.String() + "-" + expression.ElementId)
		variables, synced, err := s.syncParallel(ctx, tx, hash, expression.ParallelSyncQuantity, message.Variables)
		if err != nil || !synced {
			return err
		}
		message.Variables = variables
		return s.moveNext(ctx, tx, message, expression.ElementId, moveComplete)

	case bpm.ActivateTypeStartSubprocess:
		return s.startChildProcess(ctx, tx, message, expression, false)

	case bpm.ActivateTypeCallActivity:
		if err := s.logHistory(ctx, tx, HistoryActivated, message, expression); err != nil {
			return err
		}
		return s.startChildProcess(ctx, tx, message, expression, !expression.PropagateChildVariables)

	case bpm.ActivateTypeStartNewProcess:
		newProcess, err := s.getProcess(expression.ProcessId)
		if err != nil {
			return err
		}
		newInstanceUid, err := uuid.NewV7()
		if err != nil {
			return err
		}
		newMessage := &instanceMessage{
			InstanceUid:     newInstanceUid,
			ProcessName:     expression.ProcessId,
			BusinessKey:     message.BusinessKey,
			CurrentPosition: newProcess.StartElement,
			Headers:         maps.Clone(message.Headers),
			Variables:       maps.Clone(message.Variables),
		}
		if err := s.startInstance(ctx, tx, newMessage); err != nil {
			return err
		}
		if err := s.moveNext(ctx, tx, newMessage, newProcess.StartElement, moveActivate); err != nil {
			return err
		}
		return s.completeElement(ctx, tx, message, expression)

	case bpm.ActivateTypeMethod:
		return s.activateMethod(ctx, tx, message, expression)

	case bpm.ActivateTypeEnd:
		if err := s.logHistory(ctx, tx, HistoryCompleted, message, expression); err != nil {
			return err
		}
		if err := s.completeInstance(ctx, tx, message); err != nil {
			return err
		}
		parentProcess, found := message.ParentProcess[message.InstanceUid]
		if !found {
			s.log.InfoContext(ctx, "process completed", "process", message.ProcessName, "instance", message.InstanceUid)
			return nil
		}
		delete(message.ParentProcess, message.InstanceUid)
		if parentProcess.RestoreVariables {
			message.Variables = maps.Clone(parentProcess.Variables)
		}
		message.InstanceUid = parentProcess.InstanceUid
		message.ProcessName = parentProcess.ProcessName
		return s.moveNext(ctx, tx, message, parentProcess.Element, moveComplete)

	default:
		return fmt.Errorf("process %q: element %q has unsupported activate type %d", message.ProcessName, expression.ElementId, activateType)
	}
}

// startChildProcess переводит токен в новый экземпляр процесса expression.ProcessId: встроенного подпроцесса
// или процесса callActivity. Родитель ждёт: End дочернего процесса вернёт токен в элемент expression через Complete.
// restoreVariables - при возврате вернуть родителю его переменные, а не переменные дочернего процесса
func (s *Engine) startChildProcess(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression, restoreVariables bool) error {
	child, err := s.getProcess(expression.ProcessId)
	if err != nil {
		return err
	}
	newInstanceUid, err := uuid.NewV7()
	if err != nil {
		return err
	}
	message.addParentProcess(newInstanceUid, expression.ElementId)
	if restoreVariables {
		parent := message.ParentProcess[newInstanceUid]
		parent.RestoreVariables = true
		parent.Variables = maps.Clone(message.Variables)
	}
	message.InstanceUid = newInstanceUid
	message.ProcessName = expression.ProcessId
	if err := s.startInstance(ctx, tx, message); err != nil {
		return err
	}
	return s.moveNext(ctx, tx, message, child.StartElement, moveActivate)
}

// completeElement завершает выполненный элемент: пишет историю и передаёт токен следующим элементам
func (s *Engine) completeElement(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) error {
	if err := s.logHistory(ctx, tx, HistoryCompleted, message, expression); err != nil {
		return err
	}
	if expression.Loop != nil {
		again, err := nextIteration(message, expression)
		if err != nil {
			return err
		}
		if again {
			return s.repeatElement(ctx, tx, message, expression)
		}
	}
	switch expression.CompleteType {
	case bpm.CompleteTypeStraight:
		for i, next := range expression.NextElements {
			branch := message
			if i < len(expression.NextElements)-1 {
				branch = message.clone()
			}
			if err := s.moveNext(ctx, tx, branch, next, moveActivate); err != nil {
				return err
			}
		}
		return nil

	case bpm.CompleteTypeScript:
		next, err := runCondition(message, expression)
		if err != nil {
			return err
		}
		return s.moveNext(ctx, tx, message, next, moveActivate)

	default:
		return fmt.Errorf("process %q: element %q has unsupported complete type %d", message.ProcessName, expression.ElementId, expression.CompleteType)
	}
}

// repeatElement активирует элемент с циклом ещё раз. Метод task и scriptTask выполняется синхронно, поэтому его
// следующее выполнение - отдельная системная задача: упавшее выполнение повторяется одно, а не весь цикл,
// и долгий цикл не держит одну транзакцию. Остальные элементы при активации только ставят задачу или ждут
func (s *Engine) repeatElement(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) error {
	if expression.ActivateType != bpm.ActivateTypeMethod {
		return s.moveNext(ctx, tx, message, expression.ElementId, moveActivate)
	}
	message.CurrentPosition = expression.ElementId
	payload, err := json.Marshal(systemTask{Type: systemLoop, Message: message})
	if err != nil {
		return err
	}
	return s.queue.PushTx(ctx, tx, queue.Task{Kind: s.systemKind, Payload: payload})
}
