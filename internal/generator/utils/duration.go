package utils

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// isoDuration - длительность ISO 8601 без лет и месяцев (их длина в днях плавает): P1W, P1DT2H, PT1M30S, PT0.5S
var isoDuration = regexp.MustCompile(`^P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:[.,]\d+)?)S)?)?$`)

// ParseISODuration разбирает timeDuration таймера BPMN в формате ISO 8601, как его пишет Camunda Modeler
func ParseISODuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	match := isoDuration.FindStringSubmatch(value)
	if match == nil || value == "P" || strings.HasSuffix(value, "T") {
		return 0, fmt.Errorf("длительность %q не в формате ISO 8601 (например PT30S, PT5M, P1DT2H), годы и месяцы не поддерживаются", value)
	}
	units := []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute}
	var total time.Duration
	for i, unit := range units {
		if match[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(match[i+1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("длительность %q: %w", value, err)
		}
		total += time.Duration(n) * unit
	}
	if seconds := match[5]; seconds != "" {
		s, err := strconv.ParseFloat(strings.Replace(seconds, ",", ".", 1), 64)
		if err != nil {
			return 0, fmt.Errorf("длительность %q: %w", value, err)
		}
		total += time.Duration(s * float64(time.Second))
	}
	if total <= 0 {
		return 0, fmt.Errorf("длительность %q должна быть больше нуля", value)
	}
	return total, nil
}

// DurationLiteral - длительность выражением Go для сгенерированного кода: 90*time.Second -> "90 * time.Second"
func DurationLiteral(d time.Duration) string {
	for _, unit := range []struct {
		d    time.Duration
		name string
	}{{time.Hour, "time.Hour"}, {time.Minute, "time.Minute"}, {time.Second, "time.Second"}, {time.Millisecond, "time.Millisecond"}} {
		if d%unit.d == 0 {
			return strconv.FormatInt(int64(d/unit.d), 10) + " * " + unit.name
		}
	}
	return "time.Duration(" + strconv.FormatInt(int64(d), 10) + ")"
}
