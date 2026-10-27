package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/queue"
)

// Статусы экземпляра процесса в bpm_process_instance.status
const (
	// InstanceRunning - экземпляр выполняется или ждёт задачу, таймер, дочерний процесс
	InstanceRunning = "running"
	// InstanceCompleted - токен дошёл до End event
	InstanceCompleted = "completed"
	// InstanceFailed - задача экземпляра упала без повторов (failed в bpm_queue) или упал дочерний экземпляр
	InstanceFailed = "failed"
	// InstanceCancelled - экземпляр отменён оператором (InstanceOperations.Cancel) вместе с дочерними
	InstanceCancelled = "cancelled"
)

// Instance - экземпляр процесса из bpm_process_instance
type Instance struct {
	InstanceUid uuid.UUID `json:"instance_uid"`
	// ProcessName - тип процесса: id процесса в BPMN, у встроенного подпроцесса "<процесс>.<подпроцесс>"
	ProcessName string `json:"process_name"`
	BusinessKey string `json:"business_key,omitempty"`
	// ParentInstanceUid - родитель встроенного подпроцесса и процесса callActivity, у корневого процесса nil
	ParentInstanceUid *uuid.UUID `json:"parent_instance_uid,omitempty"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// InstanceFilter - условия выборки экземпляров, пустые поля не фильтруют
type InstanceFilter struct {
	ProcessName string
	// Status - InstanceRunning, InstanceCompleted, InstanceFailed или InstanceCancelled
	Status string
	// BusinessKey - точное совпадение; индекса по ключу нет, поэтому лучше вместе с ProcessName или Status
	BusinessKey string
	// Before - продолжить выборку: экземпляры, запущенные раньше этого (последнего из предыдущей страницы)
	Before uuid.UUID
	// Limit - не больше MaxInstancesLimit, 0 - DefaultInstancesLimit
	Limit int
}

const (
	DefaultInstancesLimit = 100
	MaxInstancesLimit     = 1000
)

// ErrInvalidStatus - в фильтре неизвестный статус экземпляра
var ErrInvalidStatus = errors.New("invalid instance status")

// Экземпляры пишутся в bpm_process_instance вместе с историей, когда включён HistoryConfig.ToDB, и в той же tx шага
const (
	insertInstanceQuery = `
insert into bpm_process_instance (instance_uid, process_name, business_key, parent_instance_uid)
values ($1, $2, nullif($3, ''), $4)
on conflict (instance_uid) do nothing`

	touchInstanceQuery = `update bpm_process_instance set updated_at = now() where instance_uid = $1`

	completeInstanceQuery = `
update bpm_process_instance
set status = 'completed', updated_at = now()
where instance_uid = $1 and status = 'running'`

	// failInstanceQuery вставляет строку, если её нет: упал сам старт экземпляра, и его tx откатилась
	failInstanceQuery = `
insert into bpm_process_instance (instance_uid, process_name, business_key, parent_instance_uid, status)
values ($1, $2, nullif($3, ''), $4, 'failed')
on conflict (instance_uid) do update set status = 'failed', updated_at = now()
where bpm_process_instance.status = 'running'`

	failAncestorsQuery = `
update bpm_process_instance
set status = 'failed', updated_at = now()
where instance_uid = any($1) and status = 'running'`
)

// startInstance пишет строку экземпляра message.InstanceUid, в который только что вошёл токен
func (s *Engine) startInstance(ctx context.Context, tx queue.Tx, message *instanceMessage) error {
	if !enabled(s.history.ToDB) {
		return nil
	}
	if _, err := tx.Exec(ctx, insertInstanceQuery, message.InstanceUid, message.ProcessName, message.BusinessKey,
		parentInstanceUid(message)); err != nil {
		return fmt.Errorf("instance: insert: %w", err)
	}
	return nil
}

// completeInstance - токен дошёл до End event экземпляра. Упавший экземпляр остаётся failed,
// даже если до End дошла другая параллельная ветка
func (s *Engine) completeInstance(ctx context.Context, tx queue.Tx, message *instanceMessage) error {
	if !enabled(s.history.ToDB) {
		return nil
	}
	if _, err := tx.Exec(ctx, completeInstanceQuery, message.InstanceUid); err != nil {
		return fmt.Errorf("instance: complete: %w", err)
	}
	return nil
}

// failInstance помечает упавшим экземпляр message.InstanceUid и всех его предков: они ждут его End, которого не будет
func (s *Engine) failInstance(ctx context.Context, tx queue.Tx, message *instanceMessage) error {
	if !enabled(s.history.ToDB) {
		return nil
	}
	if _, err := tx.Exec(ctx, failInstanceQuery, message.InstanceUid, message.ProcessName, message.BusinessKey,
		parentInstanceUid(message)); err != nil {
		return fmt.Errorf("instance: fail: %w", err)
	}
	var ancestors []uuid.UUID
	for parent := message.ParentProcess[message.InstanceUid]; parent != nil; parent = message.ParentProcess[parent.InstanceUid] {
		ancestors = append(ancestors, parent.InstanceUid)
	}
	if len(ancestors) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, failAncestorsQuery, ancestors); err != nil {
		return fmt.Errorf("instance: fail ancestors: %w", err)
	}
	return nil
}

// systemTaskFailed - KindOptions.OnFailed системной очереди: шаг процесса упал, попыток не осталось
func (s *Engine) systemTaskFailed(ctx context.Context, tx queue.Tx, task *queue.Task) error {
	var st systemTask
	if err := json.Unmarshal(task.Payload, &st); err != nil || st.Message == nil {
		return nil
	}
	return s.failInstance(ctx, tx, st.Message)
}

// serviceTaskFailed - KindOptions.OnFailed очереди service task
func (s *Engine) serviceTaskFailed(ctx context.Context, tx queue.Tx, task *queue.Task) error {
	var message instanceMessage
	if err := json.Unmarshal(task.Payload, &message); err != nil {
		return nil
	}
	return s.failInstance(ctx, tx, &message)
}

func parentInstanceUid(message *instanceMessage) *uuid.UUID {
	if parent, found := message.ParentProcess[message.InstanceUid]; found {
		return &parent.InstanceUid
	}
	return nil
}

// InstanceCount - сколько экземпляров процесса в статусе
type InstanceCount struct {
	ProcessName string `json:"process_name"`
	Status      string `json:"status"`
	Count       int64  `json:"count"`
}

const countInstancesQuery = `select process_name, status, count(*) from bpm_process_instance group by process_name, status order by process_name, status`

// CountInstances считает экземпляры по процессам и статусам, включая дочерние ("<процесс>.<подпроцесс>" - отдельно)
func (h *HistoryReader) CountInstances(ctx context.Context) ([]InstanceCount, error) {
	rows, err := h.db.Query(ctx, countInstancesQuery)
	if err != nil {
		return nil, fmt.Errorf("instance: count: %w", err)
	}
	counts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[InstanceCount])
	if err != nil {
		return nil, fmt.Errorf("instance: count: %w", err)
	}
	return counts, nil
}

const (
	instanceColumns = `instance_uid, process_name, coalesce(business_key, ''), parent_instance_uid, status, created_at, updated_at`

	selectInstanceQuery = `select ` + instanceColumns + ` from bpm_process_instance where instance_uid = $1`
)

// GetInstance возвращает экземпляр instanceUid; ErrInstanceNotFound, если его нет
func (h *HistoryReader) GetInstance(ctx context.Context, instanceUid uuid.UUID) (*Instance, error) {
	rows, err := h.db.Query(ctx, selectInstanceQuery, instanceUid)
	if err != nil {
		return nil, fmt.Errorf("instance: select: %w", err)
	}
	defer rows.Close()
	instances, err := scanInstances(rows)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("instance %s: %w", instanceUid, ErrInstanceNotFound)
	}
	return &instances[0], nil
}

// ListInstances возвращает экземпляры по фильтру, от последних запущенных к первым
func (h *HistoryReader) ListInstances(ctx context.Context, filter InstanceFilter) ([]Instance, error) {
	switch filter.Status {
	case "", InstanceRunning, InstanceCompleted, InstanceFailed, InstanceCancelled:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, filter.Status)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultInstancesLimit
	}
	limit = min(limit, MaxInstancesLimit)

	// условия собираются из заданных полей, а не через "$1 = '' or ...": так планировщик берёт индексы по статусу и процессу
	var (
		where []string
		args  []any
	)
	add := func(condition string, arg any) {
		args = append(args, arg)
		where = append(where, strings.ReplaceAll(condition, "?", "$"+strconv.Itoa(len(args))))
	}
	if filter.ProcessName != "" {
		add("process_name = ?", filter.ProcessName)
	}
	if filter.Status != "" {
		add("status = ?", filter.Status)
	}
	if filter.BusinessKey != "" {
		add("business_key = ?", filter.BusinessKey)
	}
	if filter.Before != uuid.Nil {
		add("instance_uid < ?", filter.Before)
	}
	query := "select " + instanceColumns + " from bpm_process_instance"
	if len(where) > 0 {
		query += " where " + strings.Join(where, " and ")
	}
	args = append(args, limit)
	query += " order by instance_uid desc limit $" + strconv.Itoa(len(args))

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("instance: select: %w", err)
	}
	defer rows.Close()
	return scanInstances(rows)
}

func scanInstances(rows pgx.Rows) ([]Instance, error) {
	var instances []Instance
	for rows.Next() {
		var (
			instance Instance
			parent   uuid.NullUUID
		)
		if err := rows.Scan(&instance.InstanceUid, &instance.ProcessName, &instance.BusinessKey, &parent,
			&instance.Status, &instance.CreatedAt, &instance.UpdatedAt); err != nil {
			return nil, fmt.Errorf("instance: scan: %w", err)
		}
		if parent.Valid {
			instance.ParentInstanceUid = &parent.UUID
		}
		instances = append(instances, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("instance: rows: %w", err)
	}
	return instances, nil
}
