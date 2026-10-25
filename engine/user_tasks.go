package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// Задачи пользователей, как в Camunda 7: токен приходит в userTask, движок вставляет строку bpm_user_task с токеном
// и ждёт. Исполнитель (assignee) назначается сразу или задачу берёт в работу кандидат (Claim): пользователь из
// candidateUsers или участник группы из candidateGroups. Завершает задачу исполнитель (Complete): в одной tx задача
// помечается completed и ставится системная задача, которая ведёт токен дальше с переменными из формы

// Статусы задачи пользователя в bpm_user_task.status
const (
	UserTaskCreated   = "created"
	UserTaskCompleted = "completed"
)

var (
	ErrTaskNotFound = errors.New("user task not found")
	// ErrTaskCompleted - задача уже завершена
	ErrTaskCompleted = errors.New("user task is already completed")
	// ErrTaskClaimed - задачу уже взял в работу другой пользователь
	ErrTaskClaimed = errors.New("user task is claimed by another user")
	// ErrNotCandidate - пользователь не кандидат задачи: его нет в candidateUsers и ни одной его группы в candidateGroups
	ErrNotCandidate = errors.New("user is not a candidate for the user task")
	// ErrNotAssignee - задача не назначена пользователю: завершить или вернуть её может только исполнитель
	ErrNotAssignee = errors.New("user task is not assigned to the user")
	// ErrUserRequired - не указан пользователь
	ErrUserRequired = errors.New("user id is required")
	// ErrInvalidTaskStatus - в фильтре неизвестный статус задачи
	ErrInvalidTaskStatus = errors.New("invalid user task status")
)

// UserTask - задача пользователя из bpm_user_task
type UserTask struct {
	TaskUid         uuid.UUID  `json:"task_uid"`
	InstanceUid     uuid.UUID  `json:"instance_uid"`
	ProcessName     string     `json:"process_name"`
	ElementId       string     `json:"element_id"`
	Name            string     `json:"name,omitempty"`
	BusinessKey     string     `json:"business_key,omitempty"`
	FormKey         string     `json:"form_key,omitempty"`
	Assignee        string     `json:"assignee,omitempty"`
	CandidateUsers  []string   `json:"candidate_users"`
	CandidateGroups []string   `json:"candidate_groups"`
	Status          string     `json:"status"`
	CompletedBy     string     `json:"completed_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	// Variables - переменные процесса, когда токен пришёл в задачу: данные для формы. Только у GetTask
	Variables map[string]any `json:"variables,omitempty"`
}

// TaskFilter - условия выборки задач, пустые поля не фильтруют
type TaskFilter struct {
	// Assignee - задачи, назначенные пользователю
	Assignee string
	// CandidateUser, CandidateGroups - задачи без исполнителя, которые пользователь или его группы могут взять в работу,
	// и задачи без исполнителя и кандидатов - их может взять любой.
	// Вместе с Assignee условия объединяются через «или»: «мои задачи и задачи, которые я могу взять»
	CandidateUser   string
	CandidateGroups []string
	ProcessName     string
	InstanceUid     uuid.UUID
	BusinessKey     string
	// Status - UserTaskCreated (по умолчанию) или UserTaskCompleted
	Status string
	// Before - продолжить выборку: задачи, созданные раньше этой (последней из предыдущей страницы)
	Before uuid.UUID
	// Limit - не больше MaxTasksLimit, 0 - DefaultTasksLimit
	Limit int
}

const (
	DefaultTasksLimit = 100
	MaxTasksLimit     = 1000
)

const (
	// insertUserTaskQuery - задача, назначенная при создании, назначена в момент создания: claimed_at - время назначения
	insertUserTaskQuery = `
insert into bpm_user_task (task_uid, instance_uid, process_name, element_id, name, business_key, form_key, assignee,
                           candidate_users, candidate_groups, message, claimed_at)
values ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), nullif($8, ''), $9, $10, $11,
        case when $8 <> '' then now() end)`

	userTaskColumns = `task_uid, instance_uid, process_name, element_id, coalesce(name, ''), coalesce(business_key, ''),
coalesce(form_key, ''), coalesce(assignee, ''), candidate_users, candidate_groups, status, coalesce(completed_by, ''),
created_at, claimed_at, completed_at, updated_at`

	selectUserTaskQuery = `select ` + userTaskColumns + `, message -> 'Variables' from bpm_user_task where task_uid = $1`

	selectElementUserTasksQuery = `select ` + userTaskColumns + ` from bpm_user_task where instance_uid = $1 and element_id = $2 order by task_uid`

	lockUserTaskQuery = `
select status, coalesce(assignee, ''), candidate_users, candidate_groups, message
from bpm_user_task
where task_uid = $1
for update`

	claimUserTaskQuery = `update bpm_user_task set assignee = $2, claimed_at = now(), updated_at = now() where task_uid = $1`

	unclaimUserTaskQuery = `update bpm_user_task set assignee = null, claimed_at = null, updated_at = now() where task_uid = $1`

	completeUserTaskQuery = `
update bpm_user_task
set status = 'completed', completed_by = $2, completed_at = now(), updated_at = now()
where task_uid = $1`
)

// activateUserTask - токен пришёл в задачу пользователя: создаём её, дальше токен поведёт Complete.
// Назначение, которое не вычислилось (нет переменной, не строка), пропускается с предупреждением в логе: задача
// без исполнителя попадает в очередь кандидатов, а без них её может взять любой. Ошибка здесь остановила бы
// экземпляр навсегда - шаг повторялся бы с той же ошибкой, а завершивший прошлую задачу о ней не узнал бы
func (s *Engine) activateUserTask(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) error {
	message.CurrentPosition = expression.ElementId
	assignment := expression.UserTask
	if assignment == nil {
		assignment = &bpm.UserTask{}
	}
	assignees, problems := resolveAssignment(message.Variables, "assignee", []string{assignment.Assignee})
	if len(assignees) > 1 {
		problems = append(problems, fmt.Sprintf("assignee: one user expected, got %v", assignees))
		assignees = nil
	}
	candidateUsers, userProblems := resolveAssignment(message.Variables, "candidateUsers", assignment.CandidateUsers)
	candidateGroups, groupProblems := resolveAssignment(message.Variables, "candidateGroups", assignment.CandidateGroups)
	if problems = append(append(problems, userProblems...), groupProblems...); len(problems) > 0 {
		s.log.WarnContext(ctx, "user task assignment skipped", "process", message.ProcessName, "instance", message.InstanceUid,
			"element", expression.ElementId, "problems", problems)
	}
	taskUid, err := uuid.NewV7()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, insertUserTaskQuery, taskUid, message.InstanceUid, message.ProcessName, expression.ElementId,
		assignment.Name, message.BusinessKey, assignment.FormKey, strings.Join(assignees, ""), candidateUsers, candidateGroups,
		string(payload)); err != nil {
		return fmt.Errorf("user task: insert: %w", err)
	}
	return s.logHistory(ctx, tx, HistoryActivated, message, expression)
}

// resolveAssignment подставляет переменные "${path}" и убирает пустые значения. Переменная может содержать строку
// или список строк. Значения, которые не вычислились, пропускаются и возвращаются в problems с именем attribute
func resolveAssignment(variables map[string]any, attribute string, values []string) (result []string, problems []string) {
	result = []string{}
	for _, value := range values {
		path, isVariable := strings.CutPrefix(value, "${")
		if !isVariable || !strings.HasSuffix(path, "}") {
			if value != "" {
				result = append(result, value)
			}
			continue
		}
		path = strings.TrimSuffix(path, "}")
		resolved, found := lookupVariable(variables, path)
		if !found {
			problems = append(problems, fmt.Sprintf("%s: variable %q not found", attribute, path))
			continue
		}
		switch resolved := resolved.(type) {
		case nil:
		case string:
			if resolved != "" {
				result = append(result, resolved)
			}
		case []any:
			for _, item := range resolved {
				switch text := item.(type) {
				case string:
					if text != "" {
						result = append(result, text)
					}
				default:
					problems = append(problems, fmt.Sprintf("%s: variable %q: string list expected, got item %T", attribute, path, item))
				}
			}
		case []string:
			result = append(result, resolved...)
		default:
			problems = append(problems, fmt.Sprintf("%s: variable %q: string or string list expected, got %T", attribute, path, resolved))
		}
	}
	return result, problems
}

// lookupVariable - значение переменной по пути через точку во вложенных объектах
func lookupVariable(variables map[string]any, path string) (any, bool) {
	var current any = variables
	for _, name := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		if current, ok = object[name]; !ok {
			return nil, false
		}
	}
	return current, true
}

// Database - чтение и транзакции; *pgxpool.Pool подходит
type Database interface {
	Querier
	Begin(ctx context.Context) (pgx.Tx, error)
}

// UserTaskService - задачи пользователей: очередь, взять в работу, вернуть, завершить
type UserTaskService struct {
	db     Database
	engine *Engine
}

// NewUserTaskService - engine нужен, чтобы ставить в его системную очередь шаг, который ведёт токен после завершения
func NewUserTaskService(db Database, engine *Engine) *UserTaskService {
	return &UserTaskService{db: db, engine: engine}
}

// GetTask возвращает задачу с переменными процесса на момент её создания
func (u *UserTaskService) GetTask(ctx context.Context, taskUid uuid.UUID) (*UserTask, error) {
	var (
		task      UserTask
		variables []byte
	)
	err := scanUserTask(u.db.QueryRow(ctx, selectUserTaskQuery, taskUid), &task, &variables)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("user task %s: %w", taskUid, ErrTaskNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("user task: select: %w", err)
	}
	if variables != nil {
		if err := json.Unmarshal(variables, &task.Variables); err != nil {
			return nil, fmt.Errorf("user task %s: unmarshal variables: %w", taskUid, err)
		}
	}
	return &task, nil
}

// ListTasks возвращает задачи по фильтру, от последних созданных к первым
func (u *UserTaskService) ListTasks(ctx context.Context, filter TaskFilter) ([]UserTask, error) {
	status := filter.Status
	switch status {
	case "":
		status = UserTaskCreated
	case UserTaskCreated, UserTaskCompleted:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidTaskStatus, filter.Status)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultTasksLimit
	}
	limit = min(limit, MaxTasksLimit)

	var args []any
	arg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	// статус - литерал, а не параметр: иначе частичные индексы открытых задач не подходят к общему плану запроса
	where := []string{"status = '" + status + "'"}
	var who []string
	if filter.Assignee != "" {
		who = append(who, "assignee = "+arg(filter.Assignee))
	}
	var candidate []string
	if filter.CandidateUser != "" {
		candidate = append(candidate, "candidate_users @> array["+arg(filter.CandidateUser)+"]::text[]")
	}
	if len(filter.CandidateGroups) > 0 {
		candidate = append(candidate, "candidate_groups && "+arg(filter.CandidateGroups)+"::text[]")
	}
	if len(candidate) > 0 {
		// задачу без исполнителя и кандидатов может взять любой (isCandidate), поэтому она в очереди у всех
		candidate = append(candidate, "(candidate_users = '{}' and candidate_groups = '{}')")
		who = append(who, "(assignee is null and ("+strings.Join(candidate, " or ")+"))")
	}
	if len(who) > 0 {
		where = append(where, "("+strings.Join(who, " or ")+")")
	}
	if filter.ProcessName != "" {
		where = append(where, "process_name = "+arg(filter.ProcessName))
	}
	if filter.InstanceUid != uuid.Nil {
		where = append(where, "instance_uid = "+arg(filter.InstanceUid))
	}
	if filter.BusinessKey != "" {
		where = append(where, "business_key = "+arg(filter.BusinessKey))
	}
	if filter.Before != uuid.Nil {
		where = append(where, "task_uid < "+arg(filter.Before))
	}
	query := "select " + userTaskColumns + " from bpm_user_task where " + strings.Join(where, " and ") +
		" order by task_uid desc limit " + arg(limit)

	rows, err := u.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("user task: select: %w", err)
	}
	defer rows.Close()
	tasks := []UserTask{}
	for rows.Next() {
		var task UserTask
		if err := scanUserTask(rows, &task); err != nil {
			return nil, fmt.Errorf("user task: scan: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user task: rows: %w", err)
	}
	return tasks, nil
}

func scanUserTask(row pgx.Row, task *UserTask, extra ...any) error {
	return row.Scan(append([]any{&task.TaskUid, &task.InstanceUid, &task.ProcessName, &task.ElementId, &task.Name,
		&task.BusinessKey, &task.FormKey, &task.Assignee, &task.CandidateUsers, &task.CandidateGroups, &task.Status,
		&task.CompletedBy, &task.CreatedAt, &task.ClaimedAt, &task.CompletedAt, &task.UpdatedAt}, extra...)...)
}

// GetUserTasks возвращает задачи элемента elementId экземпляра instanceUid в порядке создания, открытые и завершённые:
// элемент в цикле создаёт задачу на каждый приход токена. Для истории экземпляра: кому и когда назначена задача
func (h *HistoryReader) GetUserTasks(ctx context.Context, instanceUid uuid.UUID, elementId string) ([]UserTask, error) {
	rows, err := h.db.Query(ctx, selectElementUserTasksQuery, instanceUid, elementId)
	if err != nil {
		return nil, fmt.Errorf("user task: select: %w", err)
	}
	defer rows.Close()
	tasks := []UserTask{}
	for rows.Next() {
		var task UserTask
		if err := scanUserTask(rows, &task); err != nil {
			return nil, fmt.Errorf("user task: scan: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user task: rows: %w", err)
	}
	return tasks, nil
}

// lockedTask - задача, заблокированная в tx до её изменения
type lockedTask struct {
	status          string
	assignee        string
	candidateUsers  []string
	candidateGroups []string
	message         []byte
}

func lockTask(ctx context.Context, tx pgx.Tx, taskUid uuid.UUID) (*lockedTask, error) {
	var task lockedTask
	err := tx.QueryRow(ctx, lockUserTaskQuery, taskUid).Scan(&task.status, &task.assignee, &task.candidateUsers,
		&task.candidateGroups, &task.message)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("user task %s: %w", taskUid, ErrTaskNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("user task: lock: %w", err)
	}
	if task.status != UserTaskCreated {
		return nil, fmt.Errorf("user task %s: %w", taskUid, ErrTaskCompleted)
	}
	return &task, nil
}

// isCandidate - может ли пользователь взять задачу. Задачу без кандидатов может взять любой
func (task *lockedTask) isCandidate(userId string, groups []string) bool {
	if len(task.candidateUsers) == 0 && len(task.candidateGroups) == 0 {
		return true
	}
	return slices.Contains(task.candidateUsers, userId) ||
		slices.ContainsFunc(groups, func(group string) bool { return slices.Contains(task.candidateGroups, group) })
}

// Claim берёт задачу в работу: пользователь userId становится исполнителем. groups - группы пользователя,
// по ним проверяется candidateGroups. Повторный Claim своей задачи ничего не меняет
func (u *UserTaskService) Claim(ctx context.Context, taskUid uuid.UUID, userId string, groups []string) error {
	if userId == "" {
		return ErrUserRequired
	}
	return pgx.BeginFunc(ctx, u.db, func(tx pgx.Tx) error {
		task, err := lockTask(ctx, tx, taskUid)
		if err != nil {
			return err
		}
		switch {
		case task.assignee == userId:
			return nil
		case task.assignee != "":
			return fmt.Errorf("user task %s: %w", taskUid, ErrTaskClaimed)
		case !task.isCandidate(userId, groups):
			return fmt.Errorf("user task %s: user %q: %w", taskUid, userId, ErrNotCandidate)
		}
		if _, err := tx.Exec(ctx, claimUserTaskQuery, taskUid, userId); err != nil {
			return fmt.Errorf("user task: claim: %w", err)
		}
		return nil
	})
}

// Unclaim возвращает задачу в очередь кандидатов; вернуть может только исполнитель
func (u *UserTaskService) Unclaim(ctx context.Context, taskUid uuid.UUID, userId string) error {
	if userId == "" {
		return ErrUserRequired
	}
	return pgx.BeginFunc(ctx, u.db, func(tx pgx.Tx) error {
		task, err := lockTask(ctx, tx, taskUid)
		if err != nil {
			return err
		}
		if task.assignee != userId {
			return fmt.Errorf("user task %s: user %q: %w", taskUid, userId, ErrNotAssignee)
		}
		if _, err := tx.Exec(ctx, unclaimUserTaskQuery, taskUid); err != nil {
			return fmt.Errorf("user task: unclaim: %w", err)
		}
		return nil
	})
}

// Complete завершает задачу: variables добавляются к переменным процесса, токен идёт дальше. Завершить может
// только исполнитель, поэтому задачу кандидатов сначала берут в работу (Claim). Токен поведёт системная задача
// очереди, поставленная в той же tx: ошибка следующих шагов (например, упавший метод) повторяется очередью, а не API
func (u *UserTaskService) Complete(ctx context.Context, taskUid uuid.UUID, userId string, variables map[string]any) error {
	if userId == "" {
		return ErrUserRequired
	}
	return pgx.BeginFunc(ctx, u.db, func(tx pgx.Tx) error {
		task, err := lockTask(ctx, tx, taskUid)
		if err != nil {
			return err
		}
		if task.assignee != userId {
			return fmt.Errorf("user task %s: user %q: %w", taskUid, userId, ErrNotAssignee)
		}
		var message instanceMessage
		if err := json.Unmarshal(task.message, &message); err != nil {
			return fmt.Errorf("user task %s: unmarshal message: %w", taskUid, err)
		}
		message.Variables = maps.Clone(message.Variables)
		if message.Variables == nil {
			message.Variables = make(map[string]any, len(variables))
		}
		maps.Copy(message.Variables, variables)
		payload, err := json.Marshal(systemTask{Type: systemUserTask, Message: &message})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, completeUserTaskQuery, taskUid, userId); err != nil {
			return fmt.Errorf("user task: complete: %w", err)
		}
		return u.engine.queue.PushTx(ctx, tx, queue.Task{Kind: u.engine.systemKind, Payload: payload, Key: taskUid.String()})
	})
}
