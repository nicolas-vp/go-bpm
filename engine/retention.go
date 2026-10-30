package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// deleteExpiredQuery удаляет пачку корневых экземпляров, завершённых или отменённых больше $1 микросекунд назад, вместе с деревом
// дочерних экземпляров (встроенные подпроцессы, callActivity), их историей, историей правил и задачами пользователей.
// Корни берутся for update skip locked: несколько подов чистят одновременно, не мешая друг другу.
// Возвращает число удалённых корней: полная пачка - чистить дальше сразу
const deleteExpiredQuery = `
with recursive roots as (
    select instance_uid
    from bpm_process_instance
    where parent_instance_uid is null and status in ('completed', 'cancelled') and updated_at < now() - $1 * interval '1 microsecond'
    order by updated_at
    limit $2 for update skip locked
), tree as (
    select instance_uid from roots
    union all
    select child.instance_uid from bpm_process_instance child join tree on child.parent_instance_uid = tree.instance_uid
), history as (
    delete from bpm_history where instance_uid in (select instance_uid from tree)
), rules as (
    delete from bpm_rules_history where instance_uid in (select instance_uid from tree)
), user_tasks as (
    delete from bpm_user_task where instance_uid in (select instance_uid from tree)
), instances as (
    delete from bpm_process_instance where instance_uid in (select instance_uid from tree)
    returning parent_instance_uid
)
select count(*) from instances where parent_instance_uid is null`

// RetentionConfig - очистка истории по сроку хранения. Нулевое значение - очистка выключена
type RetentionConfig struct {
	// After - сколько хранить экземпляр после завершения или отмены: отсчёт от последнего шага (updated_at).
	// 0 или меньше - очистка выключена. Упавшие (failed) и выполняющиеся экземпляры не удаляются
	After time.Duration
	// Interval - пауза между проходами очистки, по умолчанию 1m
	Interval time.Duration
	// BatchSize - сколько корневых экземпляров удаляется в одной транзакции, по умолчанию 100
	BatchSize int
	Logger    *slog.Logger
}

// Retention удаляет историю экземпляров старше срока хранения: bpm_process_instance, bpm_history, bpm_rules_history,
// bpm_user_task. Экземпляр удаляется целиком, деревом от корня, когда корень завершён или отменён.
// Задачи bpm_queue чистит сама очередь (queue.Options.Retention)
type Retention struct {
	db     Querier
	config RetentionConfig
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

// NewRetention - очистка истории с параметрами config; запускается Start, если config.After задан
func NewRetention(db Querier, config RetentionConfig) *Retention {
	if config.Interval <= 0 {
		config.Interval = time.Minute
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 100
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Retention{db: db, config: config}
}

// Enabled - задан ли срок хранения
func (r *Retention) Enabled() bool {
	return r.config.After > 0
}

// Start запускает очистку в фоне; при выключенной очистке ничего не делает
func (r *Retention) Start(ctx context.Context) {
	if !r.Enabled() {
		return
	}
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.run(ctx)
	}()
}

// Stop останавливает очистку и ждёт, пока закончится текущая пачка
func (r *Retention) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

func (r *Retention) run(ctx context.Context) {
	for {
		deleted, err := r.DeleteExpired(ctx)
		if err != nil && ctx.Err() == nil {
			r.config.Logger.WarnContext(ctx, "bpm retention", "error", err)
		}
		// удалили полную пачку - чистим дальше сразу
		wait := r.config.Interval
		if err == nil && deleted >= r.config.BatchSize {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// DeleteExpired удаляет одну пачку экземпляров старше срока хранения и возвращает число удалённых корневых.
// Для сервиса, который чистит по своему расписанию вместо Start
func (r *Retention) DeleteExpired(ctx context.Context) (int, error) {
	if !r.Enabled() {
		return 0, nil
	}
	var deleted int
	// срок считается по часам базы, как updated_at
	if err := r.db.QueryRow(ctx, deleteExpiredQuery, r.config.After.Microseconds(), r.config.BatchSize).Scan(&deleted); err != nil {
		return 0, fmt.Errorf("retention: delete expired: %w", err)
	}
	return deleted, nil
}
