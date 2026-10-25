// Package queue - очередь задач на PostgreSQL (таблица bpm_queue), на которой работает движок.
//
// Задача выдаётся воркеру в аренду: при выдаче увеличивается attempt и run_at сдвигается на время аренды,
// поэтому задачу упавшего воркера по истечении аренды заберёт другой. Завершение задачи и её колбэки
// OnSucceeded выполняются в одной транзакции: если колбэк упал, задача не завершается и повторяется.
// Завершить задачу может только та попытка, которая её взяла: если аренда истекла и задачу уже взяли
// снова или её отменили, результат попытки отбрасывается.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrCancel - обработчик отменяет задачу: повторять её бессмысленно, и это не ошибка (например, битый payload)
	ErrCancel = errors.New("queue: cancel task")
	// ErrNonRetryable - задача падает сразу, без оставшихся повторов
	ErrNonRetryable = errors.New("queue: non-retryable error")
)

// Tx - то, что очереди и движку нужно от транзакции; pgx.Tx и pgxpool.Pool подходят
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Callback выполняется в транзакции, которая завершает задачу
type Callback func(ctx context.Context, tx Tx) error

// Handler обрабатывает задачу. Ошибка - повтор задачи, пока не кончатся попытки;
// ErrCancel и ErrNonRetryable (через errors.Is) - без повторов
type Handler func(ctx context.Context, task *Task) error

// Task - задача очереди
type Task struct {
	// Kind - вид задачи: по нему выбирается обработчик
	Kind string
	// Payload - JSON, хранится в jsonb
	Payload json.RawMessage
	// Key - ключ идемпотентности: задача, ключ которой уже есть у задачи этого вида, молча не ставится.
	// Ключ хранится, пока задачу не удалит очистка (Options.Retention). Пустой - без ключа
	Key string

	// Id, Attempt и MaxAttempts заполняются при выдаче задачи обработчику
	Id int64
	// Attempt - номер текущей попытки, с 1
	Attempt     int
	MaxAttempts int

	callbacks []Callback
}

// IsLastAttempt - после ошибки этой попытки повторов не будет
func (t *Task) IsLastAttempt() bool {
	return t.Attempt >= t.MaxAttempts
}

// OnSucceeded добавляет колбэк, который выполнится в транзакции завершения задачи после успешного обработчика
func (t *Task) OnSucceeded(callback Callback) {
	t.callbacks = append(t.callbacks, callback)
}

// PushOption - параметр постановки задачи
type PushOption func(*pushOptions)

type pushOptions struct {
	delay time.Duration
}

// WithDelay откладывает выдачу задачи на delay
func WithDelay(delay time.Duration) PushOption {
	return func(o *pushOptions) {
		o.delay = delay
	}
}
