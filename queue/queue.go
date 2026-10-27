package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// notifyChannel - канал LISTEN/NOTIFY: в payload вид задачи, которую можно брать сразу
	notifyChannel = "bpm_queue"
	// leaseMargin - запас аренды сверх AttemptTimeout: обработчик, который уложился в таймаут,
	// успевает завершить задачу до того, как её заберёт другой воркер
	leaseMargin = 10 * time.Second
	// maxErrorLength - сколько текста ошибки хранить в last_error
	maxErrorLength = 4000
)

const (
	pushQuery = `
insert into bpm_queue (kind, key, payload, run_at)
values ($1, nullif($2, ''), $3, now() + $4 * interval '1 microsecond')
on conflict (kind, key) where key is not null do nothing`

	notifyQuery = `select pg_notify($1, $2)`

	// retryFailedQuery возвращает упавшую задачу в работу с первой попытки; payload null - прежний
	retryFailedQuery = `
update bpm_queue
set status = 'pending', attempt = 0, run_at = now(), last_error = null, payload = coalesce($2, payload), updated_at = now()
where id = $1 and status = 'failed'
returning kind`

	cancelByKeyQuery = `
update bpm_queue
set status = 'cancelled', updated_at = now()
where kind = $1 and key = $2 and status = 'pending'`

	// fetchQuery выдаёт задачу в аренду до run_at = now() + lease. Подзапрос скалярный (InitPlan) и выполняется
	// один раз: с update ... from (select ... for update skip locked) один fetch под нагрузкой движка иногда брал
	// в аренду несколько задач, QueryRow возвращал одну, а остальные лежали до конца аренды и тратили попытку
	fetchQuery = `
update bpm_queue
set attempt = attempt + 1, run_at = now() + $2 * interval '1 microsecond', updated_at = now()
where id = (select id
            from bpm_queue
            where kind = $1 and status = 'pending' and run_at <= now()
            order by run_at, id
            limit 1 for update skip locked)
returning id, payload, attempt`

	// finishQuery завершает задачу, только если её всё ещё держит эта попытка
	finishQuery = `
update bpm_queue
set status = $3, last_error = $4, updated_at = now()
where id = $1 and attempt = $2 and status = 'pending'`

	retryQuery = `
update bpm_queue
set run_at = now() + $3 * interval '1 microsecond', last_error = $4, updated_at = now()
where id = $1 and attempt = $2 and status = 'pending'`

	cleanupQuery = `
delete from bpm_queue
where id in (select id from bpm_queue
             where status in ('done', 'cancelled') and updated_at < now() - $1 * interval '1 microsecond'
             limit 1000)`
)

// Статусы задачи в bpm_queue.status
const (
	StatusPending   = "pending"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// errLeaseLost - попытка больше не держит задачу: аренда истекла и задачу взяли снова, или её отменили
var errLeaseLost = errors.New("queue: task lease lost")

// Options - параметры очереди, нулевые значения заменяются значениями по умолчанию
type Options struct {
	// PollInterval - как часто воркер без задач опрашивает таблицу, ±25%. По умолчанию 1s.
	// Задачи без задержки будят воркеры сразу через LISTEN/NOTIFY, опрос нужен отложенным задачам и повторам
	PollInterval time.Duration
	// Retention - сколько хранить завершённые и отменённые задачи, по умолчанию 7 дней; отрицательное - не удалять.
	// Упавшие задачи (failed) не удаляются никогда
	Retention time.Duration
	Logger    *slog.Logger
}

// KindOptions - параметры вида задач
type KindOptions struct {
	// MaxAttempts - сколько всего попыток, по умолчанию 1
	MaxAttempts int
	// AttemptTimeout - таймаут обработчика и, отдельно, колбэков OnSucceeded. По умолчанию 30s
	AttemptTimeout time.Duration
	// RetryDelay - задержка перед повтором: растёт вдвое с каждой попыткой, со случайной половиной (equal jitter),
	// не больше MaxRetryDelay. По умолчанию 1s
	RetryDelay time.Duration
	// MaxRetryDelay - по умолчанию 5m
	MaxRetryDelay time.Duration
	// Workers - сколько задач этого вида выполняется одновременно в процессе, по умолчанию 1
	Workers int
	// OnFailed выполняется в транзакции, которая помечает задачу failed: попытки кончились или ErrNonRetryable.
	// Если колбэк упал, задача всё равно помечается failed, уже без него, а ошибка пишется в лог
	OnFailed func(ctx context.Context, tx Tx, task *Task) error
}

type kind struct {
	name    string
	handler Handler
	opts    KindOptions
	// wake будит воркеры вида по NOTIFY
	wake chan struct{}
}

// Queue - очередь задач. Виды регистрируются до Start
type Queue struct {
	pool   *pgxpool.Pool
	opts   Options
	log    *slog.Logger
	kinds  map[string]*kind
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func New(pool *pgxpool.Pool, opts Options) *Queue {
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.Retention == 0 {
		opts.Retention = 7 * 24 * time.Hour
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Queue{pool: pool, opts: opts, log: opts.Logger, kinds: make(map[string]*kind)}
}

// Register регистрирует обработчик вида задач. Повторная регистрация вида - паника: это ошибка сборки сервиса
func (q *Queue) Register(name string, handler Handler, opts KindOptions) {
	if _, found := q.kinds[name]; found {
		panic(fmt.Sprintf("queue: kind %q is registered twice", name))
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 1
	}
	if opts.AttemptTimeout <= 0 {
		opts.AttemptTimeout = 30 * time.Second
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = time.Second
	}
	if opts.MaxRetryDelay <= 0 {
		opts.MaxRetryDelay = 5 * time.Minute
	}
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	q.kinds[name] = &kind{name: name, handler: handler, opts: opts, wake: make(chan struct{}, 1)}
}

// Push ставит задачу в отдельной транзакции
func (q *Queue) Push(ctx context.Context, task Task, opts ...PushOption) error {
	return pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) error {
		return q.PushTx(ctx, tx, task, opts...)
	})
}

// InTx выполняет fn в транзакции пула очереди: задачи, поставленные в ней через PushTx, появятся вместе
// с остальными изменениями fn, только если она закоммитится
func (q *Queue) InTx(ctx context.Context, fn func(tx Tx) error) error {
	return pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) error {
		return fn(tx)
	})
}

// PushTx ставит задачу в транзакции tx: задача появится, только если tx закоммитится
func (q *Queue) PushTx(ctx context.Context, tx Tx, task Task, opts ...PushOption) error {
	var o pushOptions
	for _, opt := range opts {
		opt(&o)
	}
	if task.Payload == nil {
		task.Payload = []byte("null")
	}
	if _, err := tx.Exec(ctx, pushQuery, task.Kind, task.Key, []byte(task.Payload), o.delay.Microseconds()); err != nil {
		return fmt.Errorf("queue: push %s: %w", task.Kind, err)
	}
	if o.delay <= 0 {
		// уведомление уйдёт при коммите tx, одинаковые в одной tx схлопываются
		if _, err := tx.Exec(ctx, notifyQuery, notifyChannel, task.Kind); err != nil {
			return fmt.Errorf("queue: notify %s: %w", task.Kind, err)
		}
	}
	return nil
}

// CancelByKeyTx отменяет задачи вида kind с ключом key, которые ещё не завершены. Задачу, которую воркер уже
// выполняет, отмена тоже помечает: её результат будет отброшен
func (q *Queue) CancelByKeyTx(ctx context.Context, tx Tx, kind, key string) error {
	if _, err := tx.Exec(ctx, cancelByKeyQuery, kind, key); err != nil {
		return fmt.Errorf("queue: cancel %s/%s: %w", kind, key, err)
	}
	return nil
}

// RetryFailedTx возвращает в работу задачу id, у которой кончились попытки: статус pending, попытки с нуля.
// payload - новое содержимое задачи, nil - прежнее. Задача не failed или её нет - ErrNotFailed
func (q *Queue) RetryFailedTx(ctx context.Context, tx Tx, id int64, payload []byte) error {
	var kind string
	err := tx.QueryRow(ctx, retryFailedQuery, id, payload).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("queue: task %d: %w", id, ErrNotFailed)
	}
	if err != nil {
		return fmt.Errorf("queue: retry %d: %w", id, err)
	}
	if _, err := tx.Exec(ctx, notifyQuery, notifyChannel, kind); err != nil {
		return fmt.Errorf("queue: notify %s: %w", kind, err)
	}
	return nil
}

// Start запускает воркеры всех видов, слушатель NOTIFY и очистку. Отмена ctx или Stop останавливают выдачу
// задач; задачи, которые уже выполняются, доделываются
func (q *Queue) Start(ctx context.Context) {
	ctx, q.cancel = context.WithCancel(ctx)
	q.goWorker(func() { q.listen(ctx) })
	if q.opts.Retention > 0 {
		q.goWorker(func() { q.cleanup(ctx) })
	}
	for _, k := range q.kinds {
		for range k.opts.Workers {
			q.goWorker(func() { q.work(ctx, k) })
		}
	}
}

// Stop останавливает очередь и ждёт, пока воркеры доделают взятые задачи
func (q *Queue) Stop() {
	if q.cancel != nil {
		q.cancel()
	}
	q.wg.Wait()
}

func (q *Queue) goWorker(f func()) {
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		f()
	}()
}

func (q *Queue) work(ctx context.Context, k *kind) {
	for ctx.Err() == nil {
		task, err := q.fetch(ctx, k)
		if err != nil && ctx.Err() == nil {
			q.log.ErrorContext(ctx, "queue: fetch", "kind", k.name, "error", err)
		}
		if task != nil {
			// взятую задачу доделываем и при остановке очереди
			q.process(context.WithoutCancel(ctx), k, task)
			continue
		}
		select {
		case <-ctx.Done():
		case <-k.wake:
		case <-time.After(jitter(q.opts.PollInterval)):
		}
	}
}

func (q *Queue) fetch(ctx context.Context, k *kind) (*Task, error) {
	lease := k.opts.AttemptTimeout + leaseMargin
	task := &Task{Kind: k.name, MaxAttempts: k.opts.MaxAttempts}
	var payload []byte
	err := q.pool.QueryRow(ctx, fetchQuery, k.name, lease.Microseconds()).Scan(&task.Id, &payload, &task.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	task.Payload = payload
	return task, nil
}

func (q *Queue) process(ctx context.Context, k *kind, task *Task) {
	log := q.log.With("kind", k.name, "task", task.Id, "attempt", task.Attempt)
	// аренду теряли столько раз, что попытки кончились: воркеры падают на этой задаче
	if task.Attempt > task.MaxAttempts {
		q.finish(ctx, k, log, task, StatusFailed, errors.New("no attempts left: lease expired"))
		return
	}

	err := q.runHandler(ctx, k, task)
	if err == nil {
		err = q.complete(ctx, k, task)
		if errors.Is(err, errLeaseLost) {
			log.WarnContext(ctx, "queue: task result dropped: lease lost or task cancelled")
			return
		}
		if err == nil {
			return
		}
	}

	switch {
	case errors.Is(err, ErrCancel):
		q.finish(ctx, k, log, task, StatusCancelled, err)
	case errors.Is(err, ErrNonRetryable) || task.IsLastAttempt():
		log.ErrorContext(ctx, "queue: task failed", "error", err)
		q.finish(ctx, k, log, task, StatusFailed, err)
	default:
		delay := retryDelay(k.opts, task.Attempt)
		log.WarnContext(ctx, "queue: task will be retried", "delay", delay, "error", err)
		if _, err := q.pool.Exec(ctx, retryQuery, task.Id, task.Attempt, delay.Microseconds(), errorText(err)); err != nil {
			log.ErrorContext(ctx, "queue: retry", "error", err)
		}
	}
}

func (q *Queue) runHandler(ctx context.Context, k *kind, task *Task) (err error) {
	ctx, cancel := context.WithTimeout(ctx, k.opts.AttemptTimeout)
	defer cancel()
	defer recoverPanic(&err)
	return k.handler(ctx, task)
}

// complete завершает задачу и выполняет её колбэки в одной транзакции. Строка задачи блокируется первым
// же update, поэтому пока идут колбэки, её не возьмёт другой воркер, даже если аренда истекла
func (q *Queue) complete(ctx context.Context, k *kind, task *Task) (err error) {
	ctx, cancel := context.WithTimeout(ctx, k.opts.AttemptTimeout)
	defer cancel()
	return pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) (err error) {
		tag, err := tx.Exec(ctx, finishQuery, task.Id, task.Attempt, StatusDone, nil)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errLeaseLost
		}
		defer recoverPanic(&err)
		for _, callback := range task.callbacks {
			if err := callback(ctx, tx); err != nil {
				return err
			}
		}
		return nil
	})
}

func (q *Queue) finish(ctx context.Context, k *kind, log *slog.Logger, task *Task, status string, cause error) {
	if status == StatusFailed && k.opts.OnFailed != nil {
		err := q.failWithCallback(ctx, k, task, cause)
		if err == nil {
			return
		}
		log.ErrorContext(ctx, "queue: on failed callback", "error", err)
	}
	if _, err := q.pool.Exec(ctx, finishQuery, task.Id, task.Attempt, status, errorText(cause)); err != nil {
		log.ErrorContext(ctx, "queue: finish task", "status", status, "error", err)
	}
}

// failWithCallback помечает задачу failed и выполняет OnFailed в одной транзакции
func (q *Queue) failWithCallback(ctx context.Context, k *kind, task *Task, cause error) error {
	ctx, cancel := context.WithTimeout(ctx, k.opts.AttemptTimeout)
	defer cancel()
	return pgx.BeginFunc(ctx, q.pool, func(tx pgx.Tx) (err error) {
		tag, err := tx.Exec(ctx, finishQuery, task.Id, task.Attempt, StatusFailed, errorText(cause))
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		defer recoverPanic(&err)
		return k.opts.OnFailed(ctx, tx, task)
	})
}

// listen будит воркеры по NOTIFY из PushTx. Пока соединения нет, воркеры работают по опросу
func (q *Queue) listen(ctx context.Context) {
	for ctx.Err() == nil {
		if err := q.listenOnce(ctx); err != nil && ctx.Err() == nil {
			q.log.WarnContext(ctx, "queue: listen", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(q.opts.PollInterval):
			}
		}
	}
}

func (q *Queue) listenOnce(ctx context.Context) error {
	pooled, err := q.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// соединение с LISTEN забираем из пула насовсем: на нём остались бы подписка и чужие уведомления
	conn := pooled.Hijack()
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "listen "+notifyChannel); err != nil {
		return err
	}
	// уведомления, отправленные до LISTEN, потеряны: задачи, поставленные в этот промежуток, забираем опросом сразу
	for _, k := range q.kinds {
		wake(k)
	}
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if k, found := q.kinds[notification.Payload]; found {
			wake(k)
		}
	}
}

// wake будит один воркер вида; если будить уже некого - сигнал не копится
func wake(k *kind) {
	select {
	case k.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) cleanup(ctx context.Context) {
	for {
		tag, err := q.pool.Exec(ctx, cleanupQuery, q.opts.Retention.Microseconds())
		if err != nil && ctx.Err() == nil {
			q.log.WarnContext(ctx, "queue: cleanup", "error", err)
		}
		// удалили полную пачку - чистим дальше сразу
		wait := time.Minute
		if err == nil && tag.RowsAffected() >= 1000 {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// retryDelay - экспоненциальная задержка с equal jitter: половина фиксирована, половина случайна
func retryDelay(opts KindOptions, attempt int) time.Duration {
	delay := opts.RetryDelay
	for i := 1; i < attempt && delay < opts.MaxRetryDelay; i++ {
		delay *= 2
	}
	delay = min(delay, opts.MaxRetryDelay)
	return delay/2 + rand.N(delay/2+1)
}

// jitter - d ±25%
func jitter(d time.Duration) time.Duration {
	return d*3/4 + rand.N(d/2+1)
}

func recoverPanic(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
	}
}

func errorText(err error) any {
	if err == nil {
		return nil
	}
	text := err.Error()
	if len(text) > maxErrorLength {
		text = strings.ToValidUTF8(text[:maxErrorLength], "")
	}
	return text
}
