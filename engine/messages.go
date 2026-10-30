package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// Сообщения, как в Camunda: токен в receive task или промежуточном событии сообщения подписывается на сообщение
// по имени и ключу корреляции (по умолчанию - бизнес-ключ экземпляра), CorrelateMessage продолжает подписанные токены.
// Сообщение, которое пришло раньше подписки, может подождать её в bpm_message (WithMessageTTL): ответ внешней
// системы часто приходит раньше, чем токен дойдёт до ожидания. Подписка и сообщение с одним именем и ключом
// сериализуются advisory lock в транзакции, поэтому не разминутся: одна из сторон видит другую

// ErrMessageNotCorrelated - сообщение никого не ждёт, а ждать подписку ему не разрешили (WithMessageTTL)
var ErrMessageNotCorrelated = errors.New("message not correlated: no subscription")

// ErrMessageRequired - у сообщения нет имени или ключа корреляции
var ErrMessageRequired = errors.New("message name and correlation key are required")

const (
	// lockMessageQuery - блокировка имени и ключа сообщения до конца транзакции
	lockMessageQuery = `select pg_advisory_xact_lock(hashtext($1), hashtext($2))`

	insertSubscriptionQuery = `
insert into bpm_message_subscription (subscription_uid, message_name, correlation_key, instance_uid, process_name, element_id, message)
values ($1, $2, $3, $4, $5, $6, $7)`

	takeSubscriptionsQuery = `
delete from bpm_message_subscription
where message_name = $1 and correlation_key = $2
returning subscription_uid, message`

	bufferMessageQuery = `
insert into bpm_message (message_name, correlation_key, variables, expires_at)
values ($1, $2, $3, now() + $4 * interval '1 microsecond')`

	// takeBufferedMessageQuery - самое старое ждущее сообщение
	takeBufferedMessageQuery = `
delete from bpm_message
where id = (select id from bpm_message
            where message_name = $1 and correlation_key = $2 and expires_at > now()
            order by id limit 1)
returning variables`

	deleteSubscriptionsQuery = `delete from bpm_message_subscription where instance_uid = any($1)`

	deleteExpiredMessagesQuery = `
delete from bpm_message
where id in (select id from bpm_message where expires_at < now() limit $1)`
)

// MessageOptions - параметры CorrelateMessage, задаются через MessageOption
type MessageOptions struct {
	// TTL - сколько сообщение ждёт подписку, если сейчас его никто не ждёт; 0 - не ждёт
	TTL time.Duration
}

type MessageOption func(*MessageOptions)

// WithMessageTTL - сообщение, которое никто не ждёт, сохраняется на ttl и достанется первой подписке с его именем
// и ключом. Просроченные удаляет Retention
func WithMessageTTL(ttl time.Duration) MessageOption {
	return func(opts *MessageOptions) {
		opts.TTL = ttl
	}
}

// CorrelateMessage передаёт сообщение name с ключом корреляции correlationKey всем токенам, которые его ждут:
// variables добавляются к их переменным, токены идут дальше через очередь. Возвращает, скольким токенам оно
// досталось. Никто не ждёт: с WithMessageTTL сообщение сохраняется до подписки (0, nil), без него - ErrMessageNotCorrelated
func (s *Engine) CorrelateMessage(ctx context.Context, name, correlationKey string, variables map[string]any, opts ...MessageOption) (int, error) {
	var correlated int
	err := s.queue.InTx(ctx, func(tx queue.Tx) error {
		var err error
		correlated, err = s.CorrelateMessageTx(ctx, tx, name, correlationKey, variables, opts...)
		return err
	})
	return correlated, err
}

// CorrelateMessageTx - CorrelateMessage в транзакции tx сервиса: сообщение передаётся, только если tx закоммитится
func (s *Engine) CorrelateMessageTx(ctx context.Context, tx queue.Tx, name, correlationKey string, variables map[string]any, opts ...MessageOption) (int, error) {
	if name == "" || correlationKey == "" {
		return 0, ErrMessageRequired
	}
	var options MessageOptions
	for _, opt := range opts {
		opt(&options)
	}
	if err := lockMessage(ctx, tx, name, correlationKey); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, takeSubscriptionsQuery, name, correlationKey)
	if err != nil {
		return 0, fmt.Errorf("message: take subscriptions: %w", err)
	}
	type subscription struct {
		uid     uuid.UUID
		message []byte
	}
	var subscriptions []subscription
	for rows.Next() {
		var sub subscription
		if err := rows.Scan(&sub.uid, &sub.message); err != nil {
			rows.Close()
			return 0, fmt.Errorf("message: scan subscription: %w", err)
		}
		subscriptions = append(subscriptions, sub)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("message: take subscriptions: %w", err)
	}

	for _, sub := range subscriptions {
		var message instanceMessage
		if err := json.Unmarshal(sub.message, &message); err != nil {
			return 0, fmt.Errorf("message: subscription %s: %w", sub.uid, err)
		}
		mergeVariables(&message, variables)
		payload, err := json.Marshal(systemTask{Type: systemMessage, Message: &message})
		if err != nil {
			return 0, err
		}
		if err := s.queue.PushTx(ctx, tx, queue.Task{Kind: s.systemKind, Payload: payload, Key: sub.uid.String()}); err != nil {
			return 0, err
		}
	}
	if len(subscriptions) > 0 {
		return len(subscriptions), nil
	}
	if options.TTL <= 0 {
		return 0, fmt.Errorf("message %q, key %q: %w", name, correlationKey, ErrMessageNotCorrelated)
	}
	payload, err := json.Marshal(variables)
	if err != nil {
		return 0, err
	}
	if variables == nil {
		payload = []byte("{}")
	}
	if _, err := tx.Exec(ctx, bufferMessageQuery, name, correlationKey, string(payload), options.TTL.Microseconds()); err != nil {
		return 0, fmt.Errorf("message: buffer: %w", err)
	}
	return 0, nil
}

// activateMessage - токен пришёл в элемент, который ждёт сообщение. Сообщение, которое уже ждёт в буфере,
// забирается сразу, иначе токен подписывается, и дальше его поведёт CorrelateMessage
func (s *Engine) activateMessage(ctx context.Context, tx queue.Tx, message *instanceMessage, expression *bpm.Expression) error {
	message.CurrentPosition = expression.ElementId
	if expression.Message == nil {
		return fmt.Errorf("process %q: element %q has no message", message.ProcessName, expression.ElementId)
	}
	name := expression.Message.Name
	key, err := correlationKey(message, expression.Message)
	if err != nil {
		return fmt.Errorf("process %q: element %q: %w", message.ProcessName, expression.ElementId, err)
	}
	if err := lockMessage(ctx, tx, name, key); err != nil {
		return err
	}
	if err := s.logHistory(ctx, tx, HistoryActivated, message, expression); err != nil {
		return err
	}

	var buffered []byte
	err = tx.QueryRow(ctx, takeBufferedMessageQuery, name, key).Scan(&buffered)
	switch {
	case err == nil:
		var variables map[string]any
		if err := json.Unmarshal(buffered, &variables); err != nil {
			return fmt.Errorf("message: buffered variables: %w", err)
		}
		mergeVariables(message, variables)
		return s.moveNext(ctx, tx, message, expression.ElementId, moveComplete)
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("message: take buffered: %w", err)
	}

	subscriptionUid, err := uuid.NewV7()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, insertSubscriptionQuery, subscriptionUid, name, key, message.InstanceUid, message.ProcessName,
		expression.ElementId, string(payload)); err != nil {
		return fmt.Errorf("message: subscribe: %w", err)
	}
	return nil
}

// correlationKey - ключ корреляции подписки: значение переменной, константа или бизнес-ключ экземпляра.
// Пустой ключ - ошибка: такая подписка не дождалась бы сообщения никогда
func correlationKey(message *instanceMessage, subscription *bpm.Message) (string, error) {
	key := subscription.CorrelationKey
	if key == "" {
		if message.BusinessKey == "" {
			return "", fmt.Errorf("message %q: no correlation key: instance has no business key", subscription.Name)
		}
		return message.BusinessKey, nil
	}
	path, isVariable := strings.CutPrefix(key, "${")
	if !isVariable || !strings.HasSuffix(path, "}") {
		return key, nil
	}
	path = strings.TrimSuffix(path, "}")
	value, found := lookupVariable(message.Variables, path)
	if !found {
		return "", fmt.Errorf("message %q: correlation key variable %q not found", subscription.Name, path)
	}
	switch value := value.(type) {
	case string:
		if value == "" {
			return "", fmt.Errorf("message %q: correlation key variable %q is empty", subscription.Name, path)
		}
		return value, nil
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(value), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	default:
		return "", fmt.Errorf("message %q: correlation key variable %q: string or number expected, got %T", subscription.Name, path, value)
	}
}

func lockMessage(ctx context.Context, tx queue.Tx, name, key string) error {
	if _, err := tx.Exec(ctx, lockMessageQuery, name, key); err != nil {
		return fmt.Errorf("message: lock: %w", err)
	}
	return nil
}

// mergeVariables добавляет переменные сообщения к переменным токена
func mergeVariables(message *instanceMessage, variables map[string]any) {
	if len(variables) == 0 {
		return
	}
	merged := make(map[string]any, len(message.Variables)+len(variables))
	maps.Copy(merged, message.Variables)
	maps.Copy(merged, variables)
	message.Variables = merged
}
