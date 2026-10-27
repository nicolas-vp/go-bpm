package engine

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Динамика процесса: экземпляры по дням запуска и чем они закончились, длительность завершённых.
// Экземпляры считаются по дню запуска (когорта): «упал» у дня 5 октября - из запущенных 5 октября упал, когда бы
// это ни случилось. Так у дня одна строка, и сумма исходов равна числу запущенных

// MaxStatsDays - самый длинный период статистики
const MaxStatsDays = 366

// ErrInvalidPeriod - период пустой, перевёрнутый или длиннее MaxStatsDays
var ErrInvalidPeriod = errors.New("invalid period")

// StatsFilter - процесс и период запуска экземпляров
type StatsFilter struct {
	// ProcessName - процесс; входят и его экземпляры, вызванные callActivity другого процесса
	ProcessName string
	// From, To - когда запущен экземпляр: From включительно, To - нет
	From, To time.Time
	// Location - часовой пояс, в котором режутся дни; nil - UTC
	Location *time.Location
}

// InstanceStats - экземпляры, запущенные за день или период, по текущему статусу и длительность завершённых
type InstanceStats struct {
	Started   int `json:"started"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	// MedianMs, P95Ms - длительность завершённых: от старта до End event; nil - завершённых нет
	MedianMs *float64 `json:"median_ms"`
	P95Ms    *float64 `json:"p95_ms"`
}

// DayStats - экземпляры, запущенные за день
type DayStats struct {
	// Day - день в часовом поясе фильтра, "2026-10-27"
	Day string `json:"day"`
	InstanceStats
}

// ProcessStats - статистика процесса за период: итог и каждый день, дни без экземпляров - с нулями
type ProcessStats struct {
	ProcessName string        `json:"process_name"`
	Total       InstanceStats `json:"total"`
	Days        []DayStats    `json:"days"`
}

// selectStatsQuery - строки по дням и строка итога (day null): медиану периода из медиан дней не собрать
const selectStatsQuery = `
select (created_at at time zone $2)::date::text,
       count(*),
       count(*) filter (where status = 'running'),
       count(*) filter (where status = 'completed'),
       count(*) filter (where status = 'failed'),
       count(*) filter (where status = 'cancelled'),
       percentile_cont(0.5) within group (order by extract(epoch from updated_at - created_at))
           filter (where status = 'completed') * 1000,
       percentile_cont(0.95) within group (order by extract(epoch from updated_at - created_at))
           filter (where status = 'completed') * 1000
from bpm_process_instance
where process_name = $1 and created_at >= $3 and created_at < $4
group by grouping sets (((created_at at time zone $2)::date), ())`

// GetProcessStats считает экземпляры процесса filter.ProcessName, запущенные в период, по дням. Длительность
// завершённого экземпляра - updated_at - created_at: после End event строка экземпляра не меняется.
// Неизвестный процесс - ErrProcessNotFound, неверный период - ErrInvalidPeriod
func (h *HistoryReader) GetProcessStats(ctx context.Context, filter StatsFilter) (*ProcessStats, error) {
	if _, found := h.processes[filter.ProcessName]; !found {
		return nil, fmt.Errorf("process %q: %w", filter.ProcessName, ErrProcessNotFound)
	}
	location := filter.Location
	if location == nil {
		location = time.UTC
	}
	days := statsDays(filter.From.In(location), filter.To.In(location))
	if !filter.From.Before(filter.To) || len(days) > MaxStatsDays {
		return nil, fmt.Errorf("%w: %s - %s, at most %d days", ErrInvalidPeriod, filter.From, filter.To, MaxStatsDays)
	}

	rows, err := h.db.Query(ctx, selectStatsQuery, filter.ProcessName, location.String(), filter.From, filter.To)
	if err != nil {
		return nil, fmt.Errorf("stats: select: %w", err)
	}
	defer rows.Close()
	stats := &ProcessStats{ProcessName: filter.ProcessName}
	byDay := make(map[string]InstanceStats)
	for rows.Next() {
		var (
			day *string
			row InstanceStats
		)
		if err := rows.Scan(&day, &row.Started, &row.Running, &row.Completed, &row.Failed, &row.Cancelled,
			&row.MedianMs, &row.P95Ms); err != nil {
			return nil, fmt.Errorf("stats: scan: %w", err)
		}
		if day == nil {
			stats.Total = row
		} else {
			byDay[*day] = row
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: rows: %w", err)
	}
	stats.Days = make([]DayStats, len(days))
	for i, day := range days {
		stats.Days[i] = DayStats{Day: day, InstanceStats: byDay[day]}
	}
	return stats, nil
}

// statsDays - дни периода [from, to) по календарю их часового пояса: день, в который попадает to, входит,
// только если to не его начало
func statsDays(from, to time.Time) []string {
	var days []string
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	for ; day.Before(to) && len(days) <= MaxStatsDays; day = day.AddDate(0, 0, 1) {
		days = append(days, day.Format(time.DateOnly))
	}
	return days
}
