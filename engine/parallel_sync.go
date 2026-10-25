package engine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"

	"github.com/nicolas-vp/go-bpm/queue"
)

const (
	arriveParallelSyncQuery = `
insert into bpm_parallel_sync (hash, arrived, variables)
values ($1, 1, $2::jsonb)
on conflict (hash) do update
    set arrived    = bpm_parallel_sync.arrived + 1,
        variables  = bpm_parallel_sync.variables || excluded.variables,
        updated_at = now()
returning arrived, variables`

	deleteParallelSyncQuery = `delete from bpm_parallel_sync where hash = $1`
)

// syncParallel отмечает приход ветки в точку синхронизации. Возвращает synced=true и объединённые
// переменные всех веток, когда пришла последняя из quantity веток.
func (s *Engine) syncParallel(ctx context.Context, tx queue.Tx, hash string, quantity int, variables map[string]any) (map[string]any, bool, error) {
	if quantity <= 1 {
		return variables, true, nil
	}

	payload, err := json.Marshal(variables)
	if err != nil {
		return nil, false, err
	}
	if variables == nil {
		payload = []byte("{}")
	}

	var (
		arrived       int
		mergedPayload []byte
	)
	if err := tx.QueryRow(ctx, arriveParallelSyncQuery, hash, string(payload)).Scan(&arrived, &mergedPayload); err != nil {
		return nil, false, err
	}
	if arrived < quantity {
		return nil, false, nil
	}

	if _, err := tx.Exec(ctx, deleteParallelSyncQuery, hash); err != nil {
		return nil, false, err
	}
	var merged map[string]any
	if err := json.Unmarshal(mergedPayload, &merged); err != nil {
		return nil, false, err
	}
	return merged, true, nil
}

// syncKey - ключ строки bpm_parallel_sync: точка слияния элемента в экземпляре
func syncKey(value string) string {
	hash := md5.Sum([]byte(value))
	return hex.EncodeToString(hash[:])
}
