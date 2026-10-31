// Package engine - BPM-движок: исполняет процессы, сгенерированные go-bpm-gen, на очереди задач PostgreSQL.
//
// Каждый шаг процесса - задача очереди. Все побочные эффекты шага (новые задачи, счётчики слияния веток, история)
// пишутся в транзакции, которая завершает задачу, поэтому шаг применяется атомарно или не применяется вовсе.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// DefaultKindPrefix - префикс видов задач движка в очереди по умолчанию
const DefaultKindPrefix = "bpm"

// ErrProcessNotFound - процесса с таким id нет среди процессов движка
var ErrProcessNotFound = errors.New("process not found")

// Queue - то, что движку нужно от очереди; *queue.Queue подходит
type Queue interface {
	Register(kind string, handler queue.Handler, opts queue.KindOptions)
	// InTx выполняет fn в транзакции: в ней StartProcess пишет экземпляр и ставит задачу старта
	InTx(ctx context.Context, fn func(tx queue.Tx) error) error
	PushTx(ctx context.Context, tx queue.Tx, task queue.Task, opts ...queue.PushOption) error
	// CancelByKeyTx отменяет задачу, которая ещё не завершена: прерванная задача и таймеры
	CancelByKeyTx(ctx context.Context, tx queue.Tx, kind, key string) error
	// RetryFailedTx возвращает в работу упавшую задачу экземпляра, payload nil - прежний
	RetryFailedTx(ctx context.Context, tx queue.Tx, id int64, payload []byte) error
}

// Config - параметры движка, нулевые значения заменяются значениями по умолчанию
type Config struct {
	// KindPrefix - префикс видов задач в очереди: системная очередь <prefix>.system, service task <prefix>.task.<jobType>.
	// Разные префиксы разводят по одной таблице bpm_queue экземпляры с разным кодом процессов. По умолчанию "bpm"
	KindPrefix string
	// SystemWorkers - сколько системных задач (шаги процессов, таймеры, старты) выполняется одновременно, по умолчанию 4.
	// Синхронные методы task и scriptTask выполняются в шагах системной очереди
	SystemWorkers int
	// SystemMaxAttempts - попытки шага процесса: упавший метод откатывает шаг, и очередь его повторяет. По умолчанию 10
	SystemMaxAttempts int
	// AttemptTimeout - таймаут одной попытки задачи, по умолчанию 30s
	AttemptTimeout time.Duration
	// RetryDelay - базовая задержка повтора, растёт вдвое с каждой попыткой; по умолчанию 1s
	RetryDelay time.Duration
	// TaskWorkers - сколько задач одного jobType выполняется одновременно, по умолчанию 1
	TaskWorkers int

	History HistoryConfig
	// RulesHistory - история бизнес-правил (таблица bpm_rules_history), флаги отдельные от History
	RulesHistory HistoryConfig
	// Hooks - точки подключения: события элементов, обёртка вызовов кода сервиса, падения экземпляров
	Hooks Hooks
	// AI - модель для AI task (service task с gobpm:aiTask). Без неё задачи AI task падают, как service task
	// без обработчика. Реализация - в сервисе: вызов нужной модели (Claude, OpenAI, локальная)
	AI AIClient
	// AIAttemptTimeout - таймаут одной попытки AI task вместо AttemptTimeout: ответ модели бывает дольше
	// обычного обработчика. По умолчанию 5m
	AIAttemptTimeout time.Duration
	Logger           *slog.Logger
}

type Engine struct {
	queue Queue
	// deployment - flow всех процессов и подпроцессов по id
	deployment map[string]bpm.DeploymentProcess
	// definitions - корневые процессы с названиями и BPMN, по id
	definitions []ProcessDefinition
	systemKind  string
	taskKinds   map[string]string
	history     HistoryConfig
	// rulesHistory - куда писать историю бизнес-правил: сработавшие правила решений DMN
	rulesHistory HistoryConfig
	// historyOut - куда выводится история на экран
	historyOut io.Writer
	hooks      Hooks
	// ai - модель AI task, nil - AI task в процессах нет
	ai  AIClient
	log *slog.Logger
}

// New регистрирует в очереди системный вид задач и виды service task, поэтому вызывается до queue.Start.
// processes и queues - Processes из сгенерированного bpm-gen.go и Queues из queue-gen.go:
// очереди service task с обработчиками, общие для всех процессов
func New(q Queue, config Config, processes map[string]bpm.Process, queues []bpm.Queue) (*Engine, error) {
	config = withDefaults(config)
	deployment, err := mergeFlows(processes)
	if err != nil {
		return nil, err
	}
	queues, err = sortedQueues(queues)
	if err != nil {
		return nil, err
	}
	definitions, err := catalog(processes)
	if err != nil {
		return nil, err
	}

	s := &Engine{
		queue:        q,
		deployment:   deployment,
		definitions:  definitions,
		systemKind:   config.KindPrefix + ".system",
		taskKinds:    make(map[string]string, len(queues)),
		history:      config.History,
		rulesHistory: config.RulesHistory,
		historyOut:   os.Stdout,
		hooks:        config.Hooks,
		ai:           config.AI,
		log:          config.Logger,
	}
	aiTypes := aiTaskTypes(deployment)
	if len(aiTypes) > 0 && config.AI == nil {
		s.log.Warn("processes have AI tasks but engine.Config.AI is not set, their tasks will fail",
			"task_types", slices.Sorted(maps.Keys(aiTypes)))
	}

	q.Register(s.systemKind, s.handleSystemTask, queue.KindOptions{
		MaxAttempts:    config.SystemMaxAttempts,
		AttemptTimeout: config.AttemptTimeout,
		RetryDelay:     config.RetryDelay,
		Workers:        config.SystemWorkers,
		OnFailed:       s.systemTaskFailed,
	})
	for _, taskQueue := range queues {
		kind := config.KindPrefix + ".task." + taskQueue.TypeID
		s.taskKinds[taskQueue.TypeID] = kind
		handler, attemptTimeout := taskQueue.Handler, config.AttemptTimeout
		if handler == nil && aiTypes[taskQueue.TypeID] {
			// AI task: обработчика jobType нет, задачу выполняет движок
			handler, attemptTimeout = s.aiHandler, config.AIAttemptTimeout
		}
		if handler == nil {
			s.log.Warn("no handler for task type, its tasks will fail", "task_type", taskQueue.TypeID)
		}
		q.Register(kind, s.serviceTaskHandler(taskQueue.TypeID, handler), queue.KindOptions{
			MaxAttempts:    max(taskQueue.Retries, 1),
			AttemptTimeout: attemptTimeout,
			RetryDelay:     config.RetryDelay,
			Workers:        config.TaskWorkers,
			OnFailed:       s.serviceTaskFailed,
		})
	}
	return s, nil
}

func withDefaults(config Config) Config {
	if config.KindPrefix == "" {
		config.KindPrefix = DefaultKindPrefix
	}
	if config.SystemWorkers <= 0 {
		config.SystemWorkers = 4
	}
	if config.SystemMaxAttempts <= 0 {
		config.SystemMaxAttempts = 10
	}
	if config.AttemptTimeout <= 0 {
		config.AttemptTimeout = 30 * time.Second
	}
	if config.AIAttemptTimeout <= 0 {
		config.AIAttemptTimeout = 5 * time.Minute
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = time.Second
	}
	if config.TaskWorkers <= 0 {
		config.TaskWorkers = 1
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return config
}

// StartOptions - параметры старта экземпляра процесса, задаются через StartOption
type StartOptions struct {
	// BusinessKey - текст, по которому экземпляр связан с объектом сервиса (заявкой, договором).
	// Хранится в bpm_process_instance, наследуется дочерними экземплярами и виден обработчикам в bpm.Job
	BusinessKey string
}

type StartOption func(*StartOptions)

// WithBusinessKey задаёт бизнес-ключ экземпляра
func WithBusinessKey(businessKey string) StartOption {
	return func(opts *StartOptions) {
		opts.BusinessKey = businessKey
	}
}

// ApplyStartOptions собирает параметры старта из опций
func ApplyStartOptions(opts ...StartOption) StartOptions {
	var options StartOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// StartProcess запускает экземпляр процесса processName с переменными variables и возвращает его id.
// Процесс выполняется асинхронно воркерами очереди; экземпляр виден в bpm_process_instance (при History.ToDB)
// сразу, со статусом running, даже если упадёт первый шаг
func (s *Engine) StartProcess(ctx context.Context, processName string, variables map[string]any, opts ...StartOption) (uuid.UUID, error) {
	var instanceUid uuid.UUID
	err := s.queue.InTx(ctx, func(tx queue.Tx) error {
		var err error
		instanceUid, err = s.StartProcessTx(ctx, tx, processName, variables, opts...)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	return instanceUid, nil
}

// StartProcessTx запускает экземпляр процесса в транзакции tx сервиса: процесс начнётся, только если tx
// закоммитится, - например, вместе с заказом, который он обрабатывает. tx - pgx.Tx той же базы, что у очереди
func (s *Engine) StartProcessTx(ctx context.Context, tx queue.Tx, processName string, variables map[string]any, opts ...StartOption) (uuid.UUID, error) {
	process, err := s.getProcess(processName)
	if err != nil {
		return uuid.Nil, err
	}

	instanceUid, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}

	message := &instanceMessage{
		InstanceUid:     instanceUid,
		ProcessName:     processName,
		BusinessKey:     ApplyStartOptions(opts...).BusinessKey,
		CurrentPosition: process.StartElement,
		Variables:       variables,
	}
	payload, err := json.Marshal(systemTask{Type: systemStart, Message: message})
	if err != nil {
		return uuid.Nil, err
	}

	// шаг старта вставит строку ещё раз и пропустит её (on conflict do nothing)
	if err := s.startInstance(ctx, tx, message); err != nil {
		return uuid.Nil, err
	}
	err = s.queue.PushTx(ctx, tx, queue.Task{Kind: s.systemKind, Payload: payload, Key: instanceUid.String()})
	if err != nil {
		return uuid.Nil, fmt.Errorf("push start task: %w", err)
	}
	return instanceUid, nil
}

// mergeFlows собирает flow всех процессов в одну map: ключи - id процессов и подпроцессов ("Process.SubProcess")
func mergeFlows(processes map[string]bpm.Process) (map[string]bpm.DeploymentProcess, error) {
	deployment := make(map[string]bpm.DeploymentProcess)
	for processName, process := range processes {
		for flowName, flow := range process.Flows {
			if _, found := deployment[flowName]; found {
				return nil, fmt.Errorf("process %q: flow %q is declared in several processes", processName, flowName)
			}
			deployment[flowName] = flow
		}
	}
	return deployment, nil
}

// sortedQueues возвращает копию очередей, отсортированную по TypeID. Генератор уже объединил service task
// всех процессов по jobType, поэтому повтор TypeID - ошибка: вид задач и его обработчик на TypeID одни
func sortedQueues(queues []bpm.Queue) ([]bpm.Queue, error) {
	sorted := slices.Clone(queues)
	slices.SortFunc(sorted, func(a, b bpm.Queue) int {
		return strings.Compare(a.TypeID, b.TypeID)
	})
	for i := 1; i < len(sorted); i++ {
		if sorted[i].TypeID == sorted[i-1].TypeID {
			return nil, fmt.Errorf("task type %q is declared in queues several times", sorted[i].TypeID)
		}
	}
	return sorted, nil
}

func (s *Engine) getProcess(processName string) (*bpm.DeploymentProcess, error) {
	deployment, found := s.deployment[processName]
	if !found {
		return nil, fmt.Errorf("process %q: %w", processName, ErrProcessNotFound)
	}
	return &deployment, nil
}

// addMessageToQueue ставит задачу service task. key - ключ идемпотентности, по нему граничный таймер
// отменяет задачу; пустой у задачи без таймеров
func (s *Engine) addMessageToQueue(ctx context.Context, tx queue.Tx, taskType string, message *instanceMessage, key string) error {
	kind, found := s.taskKinds[taskType]
	if !found {
		return fmt.Errorf("task type %q is not declared in queues", taskType)
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return s.queue.PushTx(ctx, tx, queue.Task{Kind: kind, Payload: payload, Key: key})
}
