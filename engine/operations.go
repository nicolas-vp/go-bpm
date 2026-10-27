package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Операции над экземплярами для поддержки: упавшие задачи экземпляра, их перезапуск (в том числе с исправленными
// переменными) и отмена экземпляра. Раньше это делалось SQL по bpm_queue и bpm_process_instance (docs/running.md)

var (
	// ErrFailureNotFound - упавшей задачи нет: её уже перезапустили, отменили или id неверный
	ErrFailureNotFound = errors.New("failed task not found")
	// ErrNotRootInstance - отменяется только корневой экземпляр: родитель дочернего ждёт его End
	ErrNotRootInstance = errors.New("instance is not a root instance")
	// ErrInstanceFinished - экземпляр уже завершён или отменён
	ErrInstanceFinished = errors.New("instance is already finished")
)

// Failure - задача очереди экземпляра, у которой кончились попытки
type Failure struct {
	TaskId int64  `json:"task_id"`
	Kind   string `json:"kind"`
	// InstanceUid, ProcessName - экземпляр токена задачи: сам экземпляр или его дочерний
	InstanceUid uuid.UUID `json:"instance_uid"`
	ProcessName string    `json:"process_name"`
	// ElementId - элемент токена, с которого начинается упавший шаг. Шаг идёт синхронно до ближайшего ожидания
	// (задача очереди, таймер, задача пользователя), поэтому упасть мог и элемент дальше: он - в тексте ошибки
	ElementId string `json:"element_id"`
	Attempts  int    `json:"attempts"`
	Error     string `json:"error"`
	// Variables - переменные токена: с ними задача выполнится при перезапуске
	Variables map[string]any `json:"variables"`
	FailedAt  time.Time      `json:"failed_at"`
}

// instanceOfTask - экземпляр токена задачи bpm_queue: у системных задач токен в payload -> 'message', у задач
// service task - в самом payload. По этому выражению индекс bpm_queue_instance_idx
const instanceOfTask = `coalesce(payload -> 'message' ->> 'InstanceUid', payload ->> 'InstanceUid')`

const (
	// selectInstanceTreeQuery - экземпляр и все его дочерние: встроенные подпроцессы и callActivity на любой глубине
	selectInstanceTreeQuery = `
with recursive tree (uid) as (
    select $1::uuid
    union
    select p.instance_uid
    from bpm_process_instance p
             join tree t on p.parent_instance_uid = t.uid
)
select uid from tree`

	selectFailuresQuery = `
select id, kind, attempt, coalesce(last_error, ''), payload, updated_at
from bpm_queue
where status = 'failed' and ` + instanceOfTask + ` = any($1::text[])
order by id`

	lockFailureQuery = `select payload from bpm_queue where id = $1 and status = 'failed' for update`

	resumeInstancesQuery = `
update bpm_process_instance
set status = 'running', updated_at = now()
where instance_uid = any($1) and status = 'failed'`

	lockRootInstanceQuery = `select status, parent_instance_uid from bpm_process_instance where instance_uid = $1 for update`

	lockInstancesQuery = `select instance_uid from bpm_process_instance where instance_uid = any($1) order by instance_uid for update`

	cancelQueueTasksQuery = `
update bpm_queue
set status = 'cancelled', updated_at = now()
where status in ('pending', 'failed') and ` + instanceOfTask + ` = any($1::text[])`

	cancelUserTasksQuery = `
update bpm_user_task
set status = 'cancelled', updated_at = now()
where status = 'created' and instance_uid = any($1)`

	deleteWaitsQuery = `delete from bpm_wait where instance_uid = any($1)`

	cancelInstancesQuery = `
update bpm_process_instance
set status = 'cancelled', updated_at = now()
where instance_uid = any($1) and status in ('running', 'failed')`
)

// InstanceOperations - упавшие задачи экземпляров, их перезапуск и отмена экземпляров
type InstanceOperations struct {
	db     Database
	engine *Engine
}

// NewInstanceOperations - engine нужен, чтобы вернуть задачу в его очередь
func NewInstanceOperations(db Database, engine *Engine) *InstanceOperations {
	return &InstanceOperations{db: db, engine: engine}
}

// ListFailures возвращает упавшие задачи экземпляра instanceUid и его дочерних экземпляров: упавший дочерний
// экземпляр помечает упавшими и родителей, а чинить нужно его задачу
func (o *InstanceOperations) ListFailures(ctx context.Context, instanceUid uuid.UUID) ([]Failure, error) {
	tree, err := instanceTree(ctx, o.db, instanceUid)
	if err != nil {
		return nil, err
	}
	rows, err := o.db.Query(ctx, selectFailuresQuery, uuidStrings(tree))
	if err != nil {
		return nil, fmt.Errorf("failures: select: %w", err)
	}
	defer rows.Close()
	failures := []Failure{}
	for rows.Next() {
		var (
			failure Failure
			payload []byte
		)
		if err := rows.Scan(&failure.TaskId, &failure.Kind, &failure.Attempts, &failure.Error, &payload, &failure.FailedAt); err != nil {
			return nil, fmt.Errorf("failures: scan: %w", err)
		}
		message, err := taskMessage(payload)
		if err != nil {
			return nil, fmt.Errorf("failures: task %d: %w", failure.TaskId, err)
		}
		failure.InstanceUid = message.InstanceUid
		failure.ProcessName = message.ProcessName
		failure.ElementId = message.CurrentPosition
		failure.Variables = message.Variables
		if failure.Variables == nil {
			failure.Variables = map[string]any{}
		}
		failures = append(failures, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failures: rows: %w", err)
	}
	return failures, nil
}

// Retry возвращает упавшую задачу taskId в очередь с первой попытки, а её экземпляр и его предков - в running.
// variables - заменить ими переменные токена (данные, на которых задача упала), nil - оставить прежние.
// Чинить код или данные нужно до перезапуска: иначе задача снова потратит все попытки
func (o *InstanceOperations) Retry(ctx context.Context, taskId int64, variables map[string]any) error {
	return pgx.BeginFunc(ctx, o.db, func(tx pgx.Tx) error {
		var payload []byte
		err := tx.QueryRow(ctx, lockFailureQuery, taskId).Scan(&payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("task %d: %w", taskId, ErrFailureNotFound)
		}
		if err != nil {
			return fmt.Errorf("failures: lock: %w", err)
		}
		message, err := taskMessage(payload)
		if err != nil {
			return fmt.Errorf("failures: task %d: %w", taskId, err)
		}
		var newPayload []byte
		if variables != nil {
			if newPayload, err = withVariables(payload, variables); err != nil {
				return fmt.Errorf("failures: task %d: %w", taskId, err)
			}
		}
		if err := o.engine.queue.RetryFailedTx(ctx, tx, taskId, newPayload); err != nil {
			return err
		}
		// предки упали вместе с экземпляром (failInstance): они ждут его End
		instances := []uuid.UUID{message.InstanceUid}
		for parent := message.ParentProcess[message.InstanceUid]; parent != nil; parent = message.ParentProcess[parent.InstanceUid] {
			instances = append(instances, parent.InstanceUid)
		}
		if _, err := tx.Exec(ctx, resumeInstancesQuery, instances); err != nil {
			return fmt.Errorf("failures: resume instances: %w", err)
		}
		return nil
	})
}

// Cancel отменяет корневой экземпляр instanceUid вместе с дочерними: незавершённые и упавшие задачи очереди
// (шаги, service task, таймеры) и открытые задачи пользователей становятся cancelled, экземпляры - cancelled.
// Строки экземпляров блокируются: шаг, который сейчас выполняется, обновляет строку своего экземпляра вместе
// с историей, поэтому он либо закоммитится до отмены (и его новые задачи отменятся), либо дождётся её и потеряет
// задачу (finish видит cancelled) с откатом. Шаг, который до строки экземпляра успел заблокировать свою строку
// bpm_wait или таймера, даёт взаимную блокировку: Postgres откатывает одну из tx. Откатился шаг - очередь повторит его,
// а задача уже отменена; откатилась отмена - она повторяется. Дочерний экземпляр - ErrNotRootInstance,
// завершённый - ErrInstanceFinished
func (o *InstanceOperations) Cancel(ctx context.Context, instanceUid uuid.UUID) (err error) {
	for attempt := 1; ; attempt++ {
		err = o.cancel(ctx, instanceUid)
		var pgErr *pgconn.PgError
		if attempt == cancelAttempts || !errors.As(err, &pgErr) || pgErr.Code != deadlockDetected {
			return err
		}
	}
}

const (
	// cancelAttempts - сколько раз пробовать отмену, если она попала во взаимную блокировку с шагом экземпляра
	cancelAttempts = 3
	// deadlockDetected - SQLSTATE взаимной блокировки
	deadlockDetected = "40P01"
)

func (o *InstanceOperations) cancel(ctx context.Context, instanceUid uuid.UUID) error {
	return pgx.BeginFunc(ctx, o.db, func(tx pgx.Tx) error {
		var (
			status string
			parent uuid.NullUUID
		)
		err := tx.QueryRow(ctx, lockRootInstanceQuery, instanceUid).Scan(&status, &parent)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("instance %s: %w", instanceUid, ErrInstanceNotFound)
		}
		if err != nil {
			return fmt.Errorf("cancel: lock instance: %w", err)
		}
		if parent.Valid {
			return fmt.Errorf("instance %s: parent %s: %w", instanceUid, parent.UUID, ErrNotRootInstance)
		}
		if status == InstanceCompleted || status == InstanceCancelled {
			return fmt.Errorf("instance %s is %s: %w", instanceUid, status, ErrInstanceFinished)
		}
		tree, err := instanceTree(ctx, tx, instanceUid)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, lockInstancesQuery, tree); err != nil {
			return fmt.Errorf("cancel: lock instances: %w", err)
		}
		for _, step := range []struct {
			name, query string
			arg         any
		}{
			{"queue tasks", cancelQueueTasksQuery, uuidStrings(tree)},
			{"user tasks", cancelUserTasksQuery, tree},
			{"waits", deleteWaitsQuery, tree},
			{"instances", cancelInstancesQuery, tree},
		} {
			if _, err := tx.Exec(ctx, step.query, step.arg); err != nil {
				return fmt.Errorf("cancel: %s: %w", step.name, err)
			}
		}
		o.engine.log.InfoContext(ctx, "instance cancelled", "instance", instanceUid, "instances", len(tree))
		return nil
	})
}

func instanceTree(ctx context.Context, db Querier, instanceUid uuid.UUID) ([]uuid.UUID, error) {
	rows, err := db.Query(ctx, selectInstanceTreeQuery, instanceUid)
	if err != nil {
		return nil, fmt.Errorf("instance tree: %w", err)
	}
	tree, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("instance tree: %w", err)
	}
	return tree, nil
}

// uuidStrings - id экземпляров текстом: так их сравнивает выражение instanceOfTask
func uuidStrings(ids []uuid.UUID) []string {
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = id.String()
	}
	return list
}

// taskMessage - токен из payload задачи очереди: системной задачи (systemTask) или service task (instanceMessage)
func taskMessage(payload []byte) (*instanceMessage, error) {
	var st systemTask
	if err := json.Unmarshal(payload, &st); err == nil && st.Message != nil {
		return st.Message, nil
	}
	var message instanceMessage
	if err := json.Unmarshal(payload, &message); err != nil || message.InstanceUid == uuid.Nil {
		return nil, errors.New("payload has no instance token")
	}
	return &message, nil
}

// withVariables заменяет переменные токена в payload, остальные поля остаются как есть
func withVariables(payload []byte, variables map[string]any) ([]byte, error) {
	var task map[string]json.RawMessage
	if err := json.Unmarshal(payload, &task); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	replace := func(fields map[string]json.RawMessage) error {
		data, err := json.Marshal(variables)
		fields["Variables"] = data
		return err
	}
	raw, system := task["message"]
	if !system || string(raw) == "null" {
		if err := replace(task); err != nil {
			return nil, err
		}
		return json.Marshal(task)
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}
	if err := replace(message); err != nil {
		return nil, err
	}
	data, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	task["message"] = data
	return json.Marshal(task)
}
