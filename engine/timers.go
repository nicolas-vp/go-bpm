package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// Таймеры не держат воркер: это системные задачи очереди, отложенные через WithDelay. Они в базе,
// поэтому переживают рестарт и срабатывают на любом поде.
//
// Задача service task с граничными таймерами гоняется со своими таймерами. Задачу, которую воркер уже
// выполняет, очередь отменить не может, поэтому победителя выбирает строка bpm_wait: её вставляет активация
// задачи, а удаляет первый из завершившихся - задача или таймер. Удаление идёт в tx шага, блокировка строки
// сериализует соперников, и проигравший видит, что строки уже нет.
const (
	insertWaitQuery = `insert into bpm_wait (wait_id, instance_uid, element_id) values ($1, $2, $3)`
	// deleteWaitQuery всегда возвращает строку: 1 - строку удалили мы, 0 - её уже удалил соперник
	deleteWaitQuery = `with deleted as (delete from bpm_wait where wait_id = $1 returning 1) select count(*) from deleted`
)

// timerKey - ключ идемпотентности задачи таймера. Включает wait_id, поэтому уникален на каждую активацию задачи:
// очередь молча пропускает задачу с ключом, который ещё хранится в очереди
func timerKey(waitId uuid.UUID, boundaryId string) string {
	return waitId.String() + "/" + boundaryId
}

// pushTimer ставит системную задачу Timer, которую очередь выдаст через delay.
// boundaryId пустой у промежуточного таймера: тогда по срабатыванию завершается message.CurrentPosition
func (s *Engine) pushTimer(ctx context.Context, tx queue.Tx, message *instanceMessage, boundaryId string, delay time.Duration, key string) error {
	payload, err := json.Marshal(systemTask{Type: systemTimer, Message: message, Boundary: boundaryId, Delay: delay})
	if err != nil {
		return err
	}
	task := queue.Task{Kind: s.systemKind, Payload: payload, Key: key}
	return s.queue.PushTx(ctx, tx, task, queue.WithDelay(delay))
}

// activateTimer - токен пришёл в промежуточный таймер: ставим отложенную задачу, дальше пойдёт fireTimer
func (s *Engine) activateTimer(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) error {
	message.CurrentPosition = expression.ElementId
	if err := s.pushTimer(ctx, tx, message, "", expression.Timer, ""); err != nil {
		return err
	}
	return s.logHistory(ctx, tx, HistoryActivated, message, expression)
}

// fireTimer - сработал таймер из системной задачи
func (s *Engine) fireTimer(ctx context.Context, tx queue.Tx, message *instanceMessage, boundaryId string) error {
	if boundaryId == "" {
		return s.moveNext(ctx, tx, message, message.CurrentPosition, moveComplete)
	}
	task, err := s.getExpression(message.ProcessName, message.CurrentPosition)
	if err != nil {
		return err
	}
	for _, boundary := range task.Timers() {
		if boundary.ElementId == boundaryId {
			return s.leaveByBoundary(ctx, tx, message, task, &boundary, nil)
		}
	}
	return fmt.Errorf("process %q: element %q has no timer %q", message.ProcessName, task.ElementId, boundaryId)
}

// startWait вызывается при активации service task с граничными таймерами: вставляет строку bpm_wait
// и ставит таймеры. Возвращает ключ, с которым ставится сама задача, чтобы таймер мог её отменить
func (s *Engine) startWait(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) (string, error) {
	timers := expression.Timers()
	if len(timers) == 0 {
		return "", nil
	}
	waitId := uuid.New()
	if _, err := tx.Exec(ctx, insertWaitQuery, waitId, message.InstanceUid, expression.ElementId); err != nil {
		return "", fmt.Errorf("insert wait: %w", err)
	}
	message.WaitId = waitId
	for _, timer := range timers {
		if err := s.pushTimer(ctx, tx, message, timer.ElementId, timer.Timer, timerKey(waitId, timer.ElementId)); err != nil {
			return "", err
		}
	}
	return waitId.String(), nil
}

// finishWait вызывается, когда задача с ожиданием завершилась сама или по граничному событию.
// false - соперник уже увёл токен, шаг ничего не должен делать. Победитель отменяет задачу и таймеры,
// которые ещё не взяты в работу; взятые в работу проиграют на удалении строки
func (s *Engine) finishWait(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) (bool, error) {
	if message.WaitId == uuid.Nil {
		return true, nil
	}
	waitId := message.WaitId
	var deleted int
	if err := tx.QueryRow(ctx, deleteWaitQuery, waitId).Scan(&deleted); err != nil {
		return false, fmt.Errorf("delete wait: %w", err)
	}
	if deleted == 0 {
		s.log.InfoContext(ctx, "element already left by another event", "process", message.ProcessName,
			"instance", message.InstanceUid, "element", expression.ElementId)
		return false, nil
	}
	message.WaitId = uuid.Nil
	if kind, found := s.taskKinds[expression.TaskType]; found {
		if err := s.queue.CancelByKeyTx(ctx, tx, kind, waitId.String()); err != nil {
			return false, fmt.Errorf("cancel task: %w", err)
		}
	}
	for _, timer := range expression.Timers() {
		if err := s.queue.CancelByKeyTx(ctx, tx, s.systemKind, timerKey(waitId, timer.ElementId)); err != nil {
			return false, fmt.Errorf("cancel timer %q: %w", timer.ElementId, err)
		}
	}
	return true, nil
}

// leaveByBoundary прерывает задачу task и ведёт токен по граничному событию boundary.
// cause - ошибка задачи для события ошибки: её код и текст попадают в переменные процесса
func (s *Engine) leaveByBoundary(ctx context.Context, tx queue.Tx, message *instanceMessage, task *bpm.Expression, boundary *bpm.Boundary, cause error) error {
	won, err := s.finishWait(ctx, tx, message, task)
	if err != nil || !won {
		return err
	}
	if err := s.logHistory(ctx, tx, HistoryTerminated, message, task); err != nil {
		return err
	}
	if cause != nil {
		message.Variables = maps.Clone(message.Variables)
		if message.Variables == nil {
			message.Variables = make(map[string]any)
		}
		var bpmErr *bpm.Error
		if errors.As(cause, &bpmErr) {
			message.Variables[bpm.ErrorCodeVariable] = bpmErr.Code
			message.Variables[bpm.ErrorMessageVariable] = bpmErr.Message
		} else {
			message.Variables[bpm.ErrorCodeVariable] = ""
			message.Variables[bpm.ErrorMessageVariable] = cause.Error()
		}
	}
	event, err := s.getExpression(message.ProcessName, boundary.ElementId)
	if err != nil {
		return err
	}
	message.CurrentPosition = event.ElementId
	return s.completeElement(ctx, tx, message, event)
}

// errorBoundary - граничное событие, которое ловит ошибку задачи. Бизнес-ошибку bpm.Error ловит событие
// с её кодом или без кода; техническую - только событие без кода и только если technical (кончились повторы)
func errorBoundary(expression *bpm.Expression, err error, technical bool) *bpm.Boundary {
	var bpmErr *bpm.Error
	if errors.As(err, &bpmErr) {
		return expression.ErrorBoundary(bpmErr.Code)
	}
	if technical {
		return expression.ErrorBoundary("")
	}
	return nil
}

// activateMethod выполняет task, scriptTask или businessRuleTask. Граничный таймер ограничивает метод контекстом
// с дедлайном: метод синхронный, внутри tx шага, поэтому таймер срабатывает, только если метод слушает ctx.
// Вызовы решений DMN метод передаёт через bpm.RecordDecision, они пишутся в историю правил после успеха метода
func (s *Engine) activateMethod(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) error {
	decisions := &decisionRecorder{}
	methodCtx := bpm.WithDecisionRecorder(ctx, decisions.record)
	var timer *bpm.Boundary
	if timers := expression.Timers(); len(timers) > 0 {
		timer = &timers[0]
		for i := range timers {
			if timers[i].Timer < timer.Timer {
				timer = &timers[i]
			}
		}
		var cancel context.CancelFunc
		methodCtx, cancel = context.WithTimeout(methodCtx, timer.Timer)
		defer cancel()
	}

	variables := message.Variables
	err := runMethod(methodCtx, message, expression)
	// таймер сработал раньше, чем метод вернулся: результат метода не нужен, даже если он успешный
	if timer != nil && errors.Is(methodCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		message.Variables = variables
		return s.leaveByBoundary(ctx, tx, message, expression, timer, nil)
	}
	if err != nil {
		// техническая ошибка откатывает шаг, и очередь повторит его
		if boundary := errorBoundary(expression, err, false); boundary != nil {
			return s.leaveByBoundary(ctx, tx, message, expression, boundary, err)
		}
		return err
	}
	if err := s.logDecisions(ctx, tx, message, expression, decisions.results); err != nil {
		return err
	}
	return s.completeElement(ctx, tx, message, expression)
}

func (s *Engine) getExpression(processName, elementId string) (*bpm.Expression, error) {
	process, err := s.getProcess(processName)
	if err != nil {
		return nil, err
	}
	expression, found := process.Expressions[elementId]
	if !found {
		return nil, fmt.Errorf("process %q: element %q not found", processName, elementId)
	}
	return expression, nil
}
