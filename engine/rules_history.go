package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// RulesHistoryRecord - запись истории бизнес-правил: вызов решения DMN задачей бизнес-правила
type RulesHistoryRecord struct {
	// Id - номер записи в bpm_rules_history, заполняется при чтении из базы
	Id          int64     `json:"id,omitempty"`
	ProcessName string    `json:"process_name"`
	InstanceUid uuid.UUID `json:"instance_uid"`
	ElementId   string    `json:"element_id"`
	DecisionId  string    `json:"decision_id"`
	// RuleIds - id сработавших правил в порядке таблицы; пустой, если ни одно не подошло
	RuleIds   []string  `json:"rule_ids"`
	Result    any       `json:"result"`
	CreatedAt time.Time `json:"created_at"`
}

// decisionRecorder копит вызовы решений метода. Пишутся они только после успешного метода (logDecisions):
// если метод упал или его прервал граничный таймер, его результат не идёт в процесс, и правил в истории нет
type decisionRecorder struct {
	results []bpm.DecisionResult
}

func (r *decisionRecorder) record(_ context.Context, result bpm.DecisionResult) error {
	r.results = append(r.results, result)
	return nil
}

// logDecisions пишет историю бизнес-правил задачи expression по флагам RulesHistory. В базу - в tx шага,
// поэтому запись появится только вместе с переходом токена
func (s *Engine) logDecisions(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression, results []bpm.DecisionResult) error {
	toLog, toDB := enabled(s.rulesHistory.ToLog), enabled(s.rulesHistory.ToDB)
	if len(results) == 0 || !toLog && !toDB {
		return nil
	}
	for _, result := range results {
		record := RulesHistoryRecord{
			ProcessName: message.ProcessName,
			InstanceUid: message.InstanceUid,
			ElementId:   expression.ElementId,
			DecisionId:  result.DecisionId,
			RuleIds:     result.RuleIds,
			Result:      result.Result,
			CreatedAt:   time.Now(),
		}
		if record.RuleIds == nil {
			record.RuleIds = []string{}
		}
		if toLog {
			printRulesHistory(s.historyOut, record)
		}
		if toDB {
			if err := writeRulesHistoryDB(ctx, tx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func printRulesHistory(out io.Writer, record RulesHistoryRecord) {
	rules, _ := json.Marshal(record.RuleIds)
	result, err := json.Marshal(record.Result)
	if err != nil {
		result = []byte(fmt.Sprintf("%v", record.Result))
	}
	_, _ = fmt.Fprintf(out, "%s rules history: process=%q instance=%s element=%q decision=%q rules=%s result=%s\n",
		record.CreatedAt.Format(time.RFC3339Nano), record.ProcessName, record.InstanceUid, record.ElementId, record.DecisionId, rules, result)
}

const insertRulesHistoryQuery = `
insert into bpm_rules_history (process_name, instance_uid, element_id, decision_id, rule_ids, result, created_at)
values ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7)`

func writeRulesHistoryDB(ctx context.Context, tx queue.Tx, record RulesHistoryRecord) error {
	rules, err := json.Marshal(record.RuleIds)
	if err != nil {
		return fmt.Errorf("rules history: marshal rules: %w", err)
	}
	result, err := json.Marshal(record.Result)
	if err != nil {
		return fmt.Errorf("rules history: marshal result: %w", err)
	}
	if _, err := tx.Exec(ctx, insertRulesHistoryQuery, record.ProcessName, record.InstanceUid, record.ElementId,
		record.DecisionId, string(rules), string(result), record.CreatedAt); err != nil {
		return fmt.Errorf("rules history: insert: %w", err)
	}
	return nil
}

const selectRulesHistoryQuery = `
select id, process_name, instance_uid, element_id, decision_id, rule_ids, result, created_at
from bpm_rules_history
where instance_uid = $1 and element_id = $2
order by id`

// RulesHistory - вызовы решений задачей бизнес-правила и DMN этих решений
type RulesHistory struct {
	Records []RulesHistoryRecord `json:"records"`
	// DMN - XML DMN-файла по id решения из записей; решения, которого больше нет в process/, здесь нет
	DMN map[string]string `json:"dmn"`
}

// GetRulesHistory возвращает вызовы решений элементом elementId экземпляра instanceUid в порядке записи
func (h *HistoryReader) GetRulesHistory(ctx context.Context, instanceUid uuid.UUID, elementId string) (*RulesHistory, error) {
	rows, err := h.db.Query(ctx, selectRulesHistoryQuery, instanceUid, elementId)
	if err != nil {
		return nil, fmt.Errorf("rules history: select: %w", err)
	}
	defer rows.Close()
	records, err := scanRulesHistory(rows)
	if err != nil {
		return nil, err
	}
	history := &RulesHistory{Records: records, DMN: make(map[string]string)}
	for _, record := range records {
		if dmn, found := h.decisions[record.DecisionId]; found {
			history.DMN[record.DecisionId] = dmn
		}
	}
	return history, nil
}

func scanRulesHistory(rows pgx.Rows) ([]RulesHistoryRecord, error) {
	records := []RulesHistoryRecord{}
	for rows.Next() {
		var (
			record        RulesHistoryRecord
			rules, result []byte
		)
		if err := rows.Scan(&record.Id, &record.ProcessName, &record.InstanceUid, &record.ElementId, &record.DecisionId,
			&rules, &result, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("rules history: scan: %w", err)
		}
		if err := json.Unmarshal(rules, &record.RuleIds); err != nil {
			return nil, fmt.Errorf("rules history: record %d: unmarshal rules: %w", record.Id, err)
		}
		if result != nil {
			if err := json.Unmarshal(result, &record.Result); err != nil {
				return nil, fmt.Errorf("rules history: record %d: unmarshal result: %w", record.Id, err)
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rules history: rows: %w", err)
	}
	return records, nil
}
