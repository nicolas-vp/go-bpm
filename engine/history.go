package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// HistoryConfig - куда писать историю выполнения элементов процессов.
// Флаги читаются на каждый элемент, поэтому их можно менять на лету (например, из динамического конфига сервиса); nil - выключено
type HistoryConfig struct {
	// ToLog - писать историю на экран
	ToLog func() bool
	// ToDB - писать историю в базу данных
	ToDB func() bool
}

// События истории элемента
const (
	// HistoryActivated - элемент запущен и ждёт завершения: задача service task поставлена в очередь, таймер ждёт срока
	HistoryActivated = "activated"
	// HistoryCompleted - элемент выполнен
	HistoryCompleted = "completed"
	// HistoryTerminated - задачу прервало граничное событие: таймер или ошибка
	HistoryTerminated = "terminated"
)

// HistoryRecord - запись истории: событие элемента процесса
type HistoryRecord struct {
	// Id - номер записи в bpm_history, заполняется при чтении из базы
	Id          int64     `json:"id,omitempty"`
	Event       string    `json:"event"`
	InstanceUid uuid.UUID `json:"instance_uid"`
	// ParentInstanceUid - экземпляр родительского процесса для подпроцесса и callActivity, у корневого процесса nil
	ParentInstanceUid *uuid.UUID `json:"parent_instance_uid,omitempty"`
	// ParentElementId - элемент родителя (подпроцесс или callActivity), который ждёт этот экземпляр
	ParentElementId string `json:"parent_element_id,omitempty"`
	ProcessName     string `json:"process_name"`
	ElementId       string `json:"element_id"`
	ActivateType    string `json:"activate_type,omitempty"`
	// Variables - переменные процесса после события; пишутся только для старта процесса и задач (recordsVariables),
	// у остальных записей nil
	Variables map[string]any `json:"variables,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// logHistory вызывается после выполнения каждого элемента процесса (HistoryCompleted) и при запуске service task
// (HistoryActivated). Запись в базу идёт в tx шага, поэтому появится только вместе с переходом токена;
// вывод на экран при откате шага не отменяется
func (s *Engine) logHistory(ctx context.Context, tx queue.Tx, event string, message *instanceMessage, expression *bpm.Expression) error {
	toLog, toDB := enabled(s.history.ToLog), enabled(s.history.ToDB)
	if !toLog && !toDB {
		return nil
	}
	record := HistoryRecord{
		Event:        event,
		InstanceUid:  message.InstanceUid,
		ProcessName:  message.ProcessName,
		ElementId:    expression.ElementId,
		ActivateType: expression.ActivateType.String(),
		CreatedAt:    time.Now(),
	}
	if parent, found := message.ParentProcess[message.InstanceUid]; found {
		record.ParentInstanceUid = &parent.InstanceUid
		record.ParentElementId = parent.Element
	}
	if s.recordsVariables(message, expression) {
		// пустая map, а не nil: nil в записи значит «переменные не записываются»
		record.Variables = make(map[string]any, len(message.Variables))
		maps.Copy(record.Variables, message.Variables)
	}
	if toLog {
		printHistory(s.historyOut, record)
	}
	if toDB {
		return s.writeHistoryDB(ctx, tx, record)
	}
	return nil
}

// recordsVariables - пишутся ли переменные для записи элемента: старт процесса (в том числе встроенного подпроцесса
// и процесса callActivity), задачи: task и scriptTask (Method), service task (Queue), callActivity,
// и граничные события - по ним видно errorCode и errorMessage ошибки задачи.
// Переходы, шлюзы и остальные события без переменных, чтобы история не росла снимком на каждый sequence flow
func (s *Engine) recordsVariables(message *instanceMessage, expression *bpm.Expression) bool {
	switch expression.ActivateType {
	case bpm.ActivateTypeMethod, bpm.ActivateTypeQueue, bpm.ActivateTypeCallActivity, bpm.ActivateTypeBoundary:
		return true
	}
	process, err := s.getProcess(message.ProcessName)
	return err == nil && process.StartElement == expression.ElementId
}

func printHistory(out io.Writer, record HistoryRecord) {
	parent := ""
	if record.ParentInstanceUid != nil {
		parent = fmt.Sprintf(" parent=%s/%s", record.ParentInstanceUid, record.ParentElementId)
	}
	variables := ""
	if record.Variables != nil {
		data, err := json.Marshal(record.Variables)
		if err != nil {
			data = []byte(fmt.Sprintf("%v", record.Variables))
		}
		variables = " variables=" + string(data)
	}
	_, _ = fmt.Fprintf(out, "%s bpm history: %s process=%q instance=%s%s element=%q type=%s%s\n",
		record.CreatedAt.Format(time.RFC3339Nano), record.Event, record.ProcessName, record.InstanceUid, parent, record.ElementId, record.ActivateType, variables)
}

const insertHistoryQuery = `
insert into bpm_history (process_name, instance_uid, parent_instance_uid, parent_element_id, element_id, event, variables, created_at)
values ($1, $2, $3, nullif($4, ''), $5, $6, $7::jsonb, $8)`

// writeHistoryDB пишет запись истории в таблицу bpm_history в tx шага
func (s *Engine) writeHistoryDB(ctx context.Context, tx queue.Tx, record HistoryRecord) error {
	var variables any // nil -> null: переменные для этого элемента не записываются
	if record.Variables != nil {
		data, err := json.Marshal(record.Variables)
		if err != nil {
			return fmt.Errorf("history: marshal variables: %w", err)
		}
		variables = string(data)
	}
	if _, err := tx.Exec(ctx, insertHistoryQuery, record.ProcessName, record.InstanceUid, record.ParentInstanceUid,
		record.ParentElementId, record.ElementId, record.Event, variables, record.CreatedAt); err != nil {
		return fmt.Errorf("history: insert: %w", err)
	}
	return nil
}

func enabled(flag func() bool) bool {
	return flag != nil && flag()
}
