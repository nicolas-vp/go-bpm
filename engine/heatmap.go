package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/bpm"
)

// Термокарта процесса: сколько выполнялись элементы диаграммы по истории экземпляров за период.
//
// Длительность прохода элемента:
//   - у элементов, которые ждут (service task, user task, промежуточный таймер, callActivity), - от activated
//     до completed или terminated;
//   - у остальных (task и scriptTask выполняются в шаге, встроенный подпроцесс, шлюзы, события) - от прихода токена,
//     то есть от последнего completed предшественника по BPMN в том же экземпляре, до своего completed.
//     Предшественник граничного события - его задача (terminated). Нет предшественника в истории (старт) - 0.
//
// Встроенный подпроцесс - свой экземпляр "<процесс>.<подпроцесс>", нарисован на диаграмме процесса: его элементы
// входят в термокарту, а сам подпроцесс считается от прихода токена до выхода. Процессы callActivity - в своих BPMN
// и не входят, callActivity считается от запуска до возврата

const (
	DefaultHeatmapInstances = 1000
	MaxHeatmapInstances     = 10000
)

// HeatmapFilter - экземпляры процесса для термокарты
type HeatmapFilter struct {
	// ProcessName - процесс; входят и его экземпляры, вызванные callActivity другого процесса
	ProcessName string
	// From, To - когда запущен экземпляр: From включительно, To - нет; нулевое время - без границы
	From, To time.Time
	// Limit - сколько последних запущенных экземпляров взять, не больше MaxHeatmapInstances; 0 - DefaultHeatmapInstances
	Limit int
}

// Heatmap - длительности элементов процесса по экземплярам за период
type Heatmap struct {
	ProcessName string `json:"process_name"`
	// Instances - сколько экземпляров процесса посчитано
	Instances int `json:"instances"`
	// Truncated - экземпляров за период больше Limit, посчитаны последние запущенные
	Truncated bool            `json:"truncated"`
	Elements  []ElementTiming `json:"elements"`
}

// ElementTiming - проходы элемента: длительности в миллисекундах
type ElementTiming struct {
	ElementId string `json:"element_id"`
	// Count - сколько раз элемент завершился (completed или terminated)
	Count int `json:"count"`
	// Active - сейчас ждут завершения: activated без completed
	Active  int     `json:"active"`
	TotalMs float64 `json:"total_ms"`
	AvgMs   float64 `json:"avg_ms"`
	MinMs   float64 `json:"min_ms"`
	MaxMs   float64 `json:"max_ms"`
}

// selectHeatmapRecordsQuery - записи экземпляров $1 и их встроенных подпроцессов ("<процесс $2>.<подпроцесс>")
// на любой глубине; ветка через callActivity обрывается
const selectHeatmapRecordsQuery = `
with recursive instances (uid) as (
    select unnest($1::uuid[])
    union
    select h.instance_uid
    from bpm_history h
             join instances i on h.parent_instance_uid = i.uid
    where starts_with(h.process_name, $2 || '.')
)
select instance_uid, element_id, event, created_at
from bpm_history
where instance_uid in (select uid from instances)
order by instance_uid, id`

// heatmapRecord - запись истории, нужная для замера
type heatmapRecord struct {
	ElementId string
	Event     string
	CreatedAt time.Time
}

// GetHeatmap считает длительности элементов процесса filter.ProcessName по экземплярам, запущенным в период.
// Неизвестный процесс - ErrProcessNotFound
func (h *HistoryReader) GetHeatmap(ctx context.Context, filter HeatmapFilter) (*Heatmap, error) {
	process, found := h.processes[filter.ProcessName]
	if !found {
		return nil, fmt.Errorf("process %q: %w", filter.ProcessName, ErrProcessNotFound)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultHeatmapInstances
	}
	limit = min(limit, MaxHeatmapInstances)

	roots, err := h.heatmapInstances(ctx, filter, limit+1)
	if err != nil {
		return nil, err
	}
	heatmap := &Heatmap{ProcessName: filter.ProcessName, Truncated: len(roots) > limit, Elements: []ElementTiming{}}
	roots = roots[:min(len(roots), limit)]
	heatmap.Instances = len(roots)
	if len(roots) == 0 {
		return heatmap, nil
	}

	rows, err := h.db.Query(ctx, selectHeatmapRecordsQuery, roots, filter.ProcessName)
	if err != nil {
		return nil, fmt.Errorf("heatmap: select history: %w", err)
	}
	defer rows.Close()
	builder := newHeatmapBuilder(process)
	var (
		current uuid.UUID
		records []heatmapRecord
	)
	for rows.Next() {
		var (
			instance uuid.UUID
			record   heatmapRecord
		)
		if err := rows.Scan(&instance, &record.ElementId, &record.Event, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("heatmap: scan: %w", err)
		}
		// записи идут по экземплярам, каждый считается отдельно
		if instance != current {
			builder.addInstance(records)
			current, records = instance, records[:0]
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("heatmap: rows: %w", err)
	}
	builder.addInstance(records)
	heatmap.Elements = builder.result()
	return heatmap, nil
}

// heatmapInstances - экземпляры процесса за период, от последних запущенных
func (h *HistoryReader) heatmapInstances(ctx context.Context, filter HeatmapFilter, limit int) ([]uuid.UUID, error) {
	args := []any{filter.ProcessName}
	where := []string{"process_name = $1"}
	if !filter.From.IsZero() {
		args = append(args, filter.From)
		where = append(where, "created_at >= $"+strconv.Itoa(len(args)))
	}
	if !filter.To.IsZero() {
		args = append(args, filter.To)
		where = append(where, "created_at < $"+strconv.Itoa(len(args)))
	}
	args = append(args, limit)
	query := "select instance_uid from bpm_process_instance where " + strings.Join(where, " and ") +
		" order by instance_uid desc limit $" + strconv.Itoa(len(args))
	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("heatmap: select instances: %w", err)
	}
	instances, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("heatmap: select instances: %w", err)
	}
	return instances, nil
}

// heatmapBuilder копит длительности элементов по экземплярам
type heatmapBuilder struct {
	// predecessors - откуда приходит токен в элемент: по sequence flow и от задачи к её граничному событию.
	// id элементов уникальны во всём BPMN-файле, поэтому потоки процесса и его подпроцессов в одной карте
	predecessors map[string][]string
	elements     map[string]*ElementTiming
}

func newHeatmapBuilder(process bpm.Process) *heatmapBuilder {
	predecessors := make(map[string][]string)
	for _, flow := range process.Flows {
		for id, expression := range flow.Expressions {
			for _, next := range expression.NextElements {
				predecessors[next] = append(predecessors[next], id)
			}
			for _, boundary := range expression.Boundaries {
				predecessors[boundary.ElementId] = append(predecessors[boundary.ElementId], id)
			}
		}
	}
	return &heatmapBuilder{predecessors: predecessors, elements: make(map[string]*ElementTiming)}
}

// addInstance добавляет записи одного экземпляра в порядке записи
func (b *heatmapBuilder) addInstance(records []heatmapRecord) {
	// activated - когда запущены ждущие элементы, по порядку: элемент в цикле может прийти снова
	activated := make(map[string][]time.Time)
	// left - когда токен последний раз ушёл из элемента
	left := make(map[string]time.Time)
	for _, record := range records {
		if record.Event == HistoryActivated {
			activated[record.ElementId] = append(activated[record.ElementId], record.CreatedAt)
			continue
		}
		var started time.Time
		if queue := activated[record.ElementId]; len(queue) > 0 {
			started, activated[record.ElementId] = queue[0], queue[1:]
		} else {
			for _, predecessor := range b.predecessors[record.ElementId] {
				if at, found := left[predecessor]; found && at.After(started) {
					started = at
				}
			}
		}
		left[record.ElementId] = record.CreatedAt
		var duration time.Duration
		if !started.IsZero() {
			duration = max(record.CreatedAt.Sub(started), 0)
		}
		b.add(record.ElementId, duration)
	}
	for id, queue := range activated {
		if len(queue) > 0 {
			b.element(id).Active += len(queue)
		}
	}
}

func (b *heatmapBuilder) add(id string, duration time.Duration) {
	element := b.element(id)
	ms := float64(duration) / float64(time.Millisecond)
	if element.Count == 0 || ms < element.MinMs {
		element.MinMs = ms
	}
	element.MaxMs = max(element.MaxMs, ms)
	element.TotalMs += ms
	element.Count++
}

func (b *heatmapBuilder) element(id string) *ElementTiming {
	element, found := b.elements[id]
	if !found {
		element = &ElementTiming{ElementId: id}
		b.elements[id] = element
	}
	return element
}

// result - элементы от самых долгих по суммарному времени
func (b *heatmapBuilder) result() []ElementTiming {
	list := make([]ElementTiming, 0, len(b.elements))
	for _, element := range b.elements {
		if element.Count > 0 {
			element.AvgMs = element.TotalMs / float64(element.Count)
		}
		list = append(list, *element)
	}
	slices.SortFunc(list, func(a, b ElementTiming) int {
		if a.TotalMs != b.TotalMs {
			if a.TotalMs > b.TotalMs {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ElementId, b.ElementId)
	})
	return list
}
