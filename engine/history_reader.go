package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/bpm"
)

const (
	historyColumns = `id, process_name, instance_uid, parent_instance_uid, coalesce(parent_element_id, ''), element_id, event, variables, created_at`

	selectHistoryQuery = `
select ` + historyColumns + `
from bpm_history
where instance_uid = $1
order by id`

	selectProcessNameQuery = `select process_name from bpm_history where instance_uid = $1 order by id limit 1`

	// selectHistoryWithChildrenQuery - история экземпляра и всех его дочерних экземпляров на любой глубине
	selectHistoryWithChildrenQuery = `
with recursive instances (uid) as (
    select $1::uuid
    union
    select h.instance_uid
    from bpm_history h
             join instances i on h.parent_instance_uid = i.uid
)
select ` + historyColumns + `
from bpm_history
where instance_uid in (select uid from instances)
order by id`
)

// Querier - чтение из базы; *pgxpool.Pool подходит
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ErrInstanceNotFound - в истории нет записей экземпляра
var ErrInstanceNotFound = errors.New("instance not found in history")

// HistoryReader читает историю выполнения процессов из bpm_history и историю бизнес-правил из bpm_rules_history.
// processes - Processes из сгенерированного bpm-gen.go: по ним берётся BPMN для диаграммы
// decisions - Decisions из bpm-gen.go: XML DMN-файла по id решения, для истории бизнес-правил
type HistoryReader struct {
	db        Querier
	processes map[string]bpm.Process
	decisions map[string]string
}

func NewHistoryReader(db Querier, processes map[string]bpm.Process, decisions map[string]string) *HistoryReader {
	return &HistoryReader{db: db, processes: processes, decisions: decisions}
}

// Diagram - BPMN процесса экземпляра и история элементов этой диаграммы
type Diagram struct {
	InstanceUid uuid.UUID       `json:"instance_uid"`
	ProcessName string          `json:"process_name"`
	BPMN        string          `json:"bpmn"`
	Records     []HistoryRecord `json:"records"`
	// Parent - откуда вызван экземпляр, nil у корневого процесса
	Parent *InstanceLink `json:"parent,omitempty"`
	// Children - экземпляры процессов callActivity этой диаграммы: они нарисованы в своих BPMN
	Children []InstanceLink `json:"children,omitempty"`
}

// InstanceLink - связанный экземпляр процесса и элемент родителя, который его запустил
type InstanceLink struct {
	InstanceUid uuid.UUID `json:"instance_uid"`
	// ProcessName - процесс связанного экземпляра; у Parent пусто, если записей родителя нет в выборке
	ProcessName string `json:"process_name,omitempty"`
	// ElementId - callActivity или подпроцесс родителя
	ElementId string    `json:"element_id"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

// GetDiagram возвращает BPMN процесса экземпляра instanceUid и историю элементов, которые есть на этой диаграмме:
// самого экземпляра и его встроенных подпроцессов. Процессы callActivity нарисованы в своих BPMN, их записи не входят
func (h *HistoryReader) GetDiagram(ctx context.Context, instanceUid uuid.UUID) (*Diagram, error) {
	records, err := h.GetHistory(ctx, instanceUid, true)
	if err != nil {
		return nil, err
	}
	diagram, err := diagramFor(instanceUid, records, h.processes)
	if err != nil || diagram.Parent == nil {
		return diagram, err
	}
	// записи родителя не входят в выборку от экземпляра вниз, имя его процесса - отдельным запросом
	err = h.db.QueryRow(ctx, selectProcessNameQuery, diagram.Parent.InstanceUid).Scan(&diagram.Parent.ProcessName)
	if err != nil {
		return nil, fmt.Errorf("history: parent %s: %w", diagram.Parent.InstanceUid, err)
	}
	return diagram, nil
}

func diagramFor(instanceUid uuid.UUID, records []HistoryRecord, processes map[string]bpm.Process) (*Diagram, error) {
	var processName string
	for _, record := range records {
		if record.InstanceUid == instanceUid {
			processName = record.ProcessName
			break
		}
	}
	if processName == "" {
		return nil, fmt.Errorf("instance %s: %w", instanceUid, ErrInstanceNotFound)
	}
	// у встроенного подпроцесса имя "<процесс>.<подпроцесс>", а нарисован он в BPMN процесса
	root, _, _ := strings.Cut(processName, ".")
	process, found := processes[root]
	if !found || process.BPMN == "" {
		return nil, fmt.Errorf("process %q: BPMN not found", root)
	}

	// на диаграмме экземпляр и его потомки через встроенные подпроцессы "<root>.<подпроцесс>";
	// ветка через callActivity обрывается, даже если вызван тот же процесс
	instanceProcess := make(map[uuid.UUID]string)
	instanceParent := make(map[uuid.UUID]uuid.UUID)
	for _, record := range records {
		instanceProcess[record.InstanceUid] = record.ProcessName
		if record.ParentInstanceUid != nil {
			instanceParent[record.InstanceUid] = *record.ParentInstanceUid
		}
	}
	onDiagram := map[uuid.UUID]bool{instanceUid: true}
	for changed := true; changed; {
		changed = false
		for instance, parent := range instanceParent {
			if !onDiagram[instance] && onDiagram[parent] && strings.HasPrefix(instanceProcess[instance], root+".") {
				onDiagram[instance] = true
				changed = true
			}
		}
	}

	diagram := &Diagram{InstanceUid: instanceUid, ProcessName: processName, BPMN: process.BPMN}
	children := make(map[uuid.UUID]bool)
	// lastActivated - последний запущенный элемент экземпляра: по нему находим callActivity для записей
	// без parent_element_id (записаны до появления колонки), дочерний экземпляр стартует сразу после activated
	lastActivated := make(map[uuid.UUID]string)
	for _, record := range records {
		if record.ParentInstanceUid != nil && record.ParentElementId == "" && !children[record.InstanceUid] {
			record.ParentElementId = lastActivated[*record.ParentInstanceUid]
		}
		if record.Event == HistoryActivated {
			lastActivated[record.InstanceUid] = record.ElementId
		}
		switch {
		case onDiagram[record.InstanceUid]:
			diagram.Records = append(diagram.Records, record)
			if record.InstanceUid == instanceUid && diagram.Parent == nil && record.ParentInstanceUid != nil {
				diagram.Parent = &InstanceLink{InstanceUid: *record.ParentInstanceUid, ElementId: record.ParentElementId}
			}
		case record.ParentInstanceUid != nil && onDiagram[*record.ParentInstanceUid] && !children[record.InstanceUid]:
			// первая запись дочернего экземпляра - его старт, записи идут по порядку id
			children[record.InstanceUid] = true
			diagram.Children = append(diagram.Children, InstanceLink{
				InstanceUid: record.InstanceUid,
				ProcessName: record.ProcessName,
				ElementId:   record.ParentElementId,
				StartedAt:   record.CreatedAt,
			})
		}
	}
	return diagram, nil
}

// GetHistory возвращает историю экземпляра instanceUid в порядке записи.
// withChildren - добавить историю дочерних экземпляров: подпроцессов и callActivity на всю глубину
func (h *HistoryReader) GetHistory(ctx context.Context, instanceUid uuid.UUID, withChildren bool) ([]HistoryRecord, error) {
	query := selectHistoryQuery
	if withChildren {
		query = selectHistoryWithChildrenQuery
	}
	rows, err := h.db.Query(ctx, query, instanceUid)
	if err != nil {
		return nil, fmt.Errorf("history: select: %w", err)
	}
	defer rows.Close()
	return scanHistory(rows)
}

func scanHistory(rows pgx.Rows) ([]HistoryRecord, error) {
	var records []HistoryRecord
	for rows.Next() {
		var (
			record    HistoryRecord
			parent    uuid.NullUUID
			variables []byte
		)
		if err := rows.Scan(&record.Id, &record.ProcessName, &record.InstanceUid, &parent, &record.ParentElementId,
			&record.ElementId, &record.Event, &variables, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("history: scan: %w", err)
		}
		if parent.Valid {
			record.ParentInstanceUid = &parent.UUID
		}
		// null - переменные для этого элемента не записываются
		if variables != nil {
			if err := json.Unmarshal(variables, &record.Variables); err != nil {
				return nil, fmt.Errorf("history: record %d: unmarshal variables: %w", record.Id, err)
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: rows: %w", err)
	}
	return records, nil
}
