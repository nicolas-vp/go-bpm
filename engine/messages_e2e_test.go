package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// messageProcesses: paid ждёт сообщение payment по бизнес-ключу, shipped - сообщение shipment по переменной orderId
func messageProcesses() map[string]bpm.Process {
	process := func(name string, subscription *bpm.Message) bpm.Process {
		return bpm.Process{Flows: map[string]bpm.DeploymentProcess{name: {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"wait"}},
			"wait":  {ElementId: "wait", ActivateType: bpm.ActivateTypeMessage, Message: subscription, NextElements: []string{"end"}},
			"end":   {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}}}}
	}
	return map[string]bpm.Process{
		"paid":    process("paid", &bpm.Message{Name: "payment"}),
		"shipped": process("shipped", &bpm.Message{Name: "shipment", CorrelationKey: "${orderId}"}),
	}
}

type messageTest struct {
	t      *testing.T
	pool   *pgxpool.Pool
	e      *engine.Engine
	reader *engine.HistoryReader
}

func newMessageTest(t *testing.T) *messageTest {
	pool := pgtest.Pool(t)
	log := slog.New(slog.DiscardHandler)
	processes := messageProcesses()
	q := queue.New(pool, queue.Options{PollInterval: 50 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{
		SystemMaxAttempts: 2,
		RetryDelay:        50 * time.Millisecond,
		History:           engine.HistoryConfig{ToDB: func() bool { return true }},
		Logger:            log,
	}, processes, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(context.Background())
	t.Cleanup(q.Stop)
	return &messageTest{t: t, pool: pool, e: e, reader: engine.NewHistoryReader(pool, processes, nil)}
}

// waitStatus ждёт статус экземпляра и возвращает переменные его последней записи с переменными
func (mt *messageTest) waitStatus(id uuid.UUID, status string) map[string]any {
	mt.t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		instance, err := mt.reader.GetInstance(ctx, id)
		if err == nil && instance.Status == status {
			break
		}
		if time.Now().After(deadline) {
			mt.t.Fatalf("instance %s: %+v, err %v, want %s", id, instance, err, status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	history, err := mt.reader.GetHistory(ctx, id, false)
	if err != nil {
		mt.t.Fatal(err)
	}
	var variables map[string]any
	for _, record := range history {
		if record.Variables != nil {
			variables = record.Variables
		}
	}
	return variables
}

// subscribed ждёт, пока экземпляр подпишется на сообщение
func (mt *messageTest) subscribed(id uuid.UUID) {
	mt.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := mt.pool.QueryRow(context.Background(), `select count(*) from bpm_message_subscription where instance_uid = $1`, id).Scan(&count); err != nil {
			mt.t.Fatal(err)
		}
		if count == 1 {
			return
		}
		if time.Now().After(deadline) {
			mt.t.Fatalf("instance %s: no subscription", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMessageCorrelation(t *testing.T) {
	mt := newMessageTest(t)
	ctx := context.Background()

	first, err := mt.e.StartProcess(ctx, "paid", map[string]any{"a": 1}, engine.WithBusinessKey("order-1"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := mt.e.StartProcess(ctx, "paid", nil, engine.WithBusinessKey("order-2"))
	if err != nil {
		t.Fatal(err)
	}
	mt.subscribed(first)
	mt.subscribed(other)

	if _, err := mt.e.CorrelateMessage(ctx, "payment", "order-9", nil); !errors.Is(err, engine.ErrMessageNotCorrelated) {
		t.Errorf("чужой ключ: err = %v, want ErrMessageNotCorrelated", err)
	}
	if _, err := mt.e.CorrelateMessage(ctx, "refund", "order-1", nil); !errors.Is(err, engine.ErrMessageNotCorrelated) {
		t.Errorf("чужое имя: err = %v, want ErrMessageNotCorrelated", err)
	}
	if n, err := mt.e.CorrelateMessage(ctx, "payment", "order-1", map[string]any{"paidAmount": 100}); err != nil || n != 1 {
		t.Fatalf("correlate: %d, %v", n, err)
	}
	variables := mt.waitStatus(first, engine.InstanceCompleted)
	if variables["paidAmount"] != float64(100) || variables["a"] != float64(1) {
		t.Errorf("variables = %v, want paidAmount from message and a from start", variables)
	}
	// второй раз то же сообщение никто не ждёт
	if _, err := mt.e.CorrelateMessage(ctx, "payment", "order-1", nil); !errors.Is(err, engine.ErrMessageNotCorrelated) {
		t.Errorf("повтор: err = %v, want ErrMessageNotCorrelated", err)
	}
	mt.waitStatus(other, engine.InstanceRunning)

	// отмена экземпляра снимает подписку
	if err := engine.NewInstanceOperations(mt.pool, mt.e).Cancel(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := mt.e.CorrelateMessage(ctx, "payment", "order-2", nil); !errors.Is(err, engine.ErrMessageNotCorrelated) {
		t.Errorf("после отмены: err = %v, want ErrMessageNotCorrelated", err)
	}
}

// TestMessageBeforeSubscription - сообщение с ttl пришло раньше, чем токен дошёл до ожидания: токен забирает его сразу.
// Ключ корреляции - числовая переменная процесса
func TestMessageBeforeSubscription(t *testing.T) {
	mt := newMessageTest(t)
	ctx := context.Background()

	if n, err := mt.e.CorrelateMessage(ctx, "shipment", "42", map[string]any{"carrier": "dhl"}, engine.WithMessageTTL(time.Minute)); err != nil || n != 0 {
		t.Fatalf("buffer: %d, %v", n, err)
	}
	// просроченное сообщение не достаётся никому
	if _, err := mt.e.CorrelateMessage(ctx, "shipment", "43", nil, engine.WithMessageTTL(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	id, err := mt.e.StartProcess(ctx, "shipped", map[string]any{"orderId": 42})
	if err != nil {
		t.Fatal(err)
	}
	if variables := mt.waitStatus(id, engine.InstanceCompleted); variables["carrier"] != "dhl" {
		t.Errorf("variables = %v, want carrier from buffered message", variables)
	}
	late, err := mt.e.StartProcess(ctx, "shipped", map[string]any{"orderId": 43})
	if err != nil {
		t.Fatal(err)
	}
	mt.subscribed(late)

	// Retention удаляет просроченное сообщение
	if _, err := engine.NewRetention(mt.pool, engine.RetentionConfig{After: time.Hour}).DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := mt.pool.QueryRow(ctx, `select count(*) from bpm_message`).Scan(&left); err != nil || left != 0 {
		t.Errorf("bpm_message: %d rows, err %v, want 0", left, err)
	}
}

// TestMessageWithoutKey - у экземпляра нет бизнес-ключа, а подписка ждёт по нему: шаг падает, экземпляр failed
func TestMessageWithoutKey(t *testing.T) {
	mt := newMessageTest(t)
	id, err := mt.e.StartProcess(context.Background(), "paid", nil)
	if err != nil {
		t.Fatal(err)
	}
	mt.waitStatus(id, engine.InstanceFailed)
}

// TestDemoMessage - демо-процесс TestMessage: receive task ждёт оплату по бизнес-ключу
func TestDemoMessage(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	svc := &services.Services{}
	processes := bpmgen.Processes(svc)
	q := queue.New(pool, queue.Options{PollInterval: 50 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{History: engine.HistoryConfig{ToDB: func() bool { return true }}, Logger: log}, processes, bpmgen.Queues(svc))
	if err != nil {
		t.Fatal(err)
	}
	q.Start(ctx)
	defer q.Stop()
	mt := &messageTest{t: t, pool: pool, e: e, reader: engine.NewHistoryReader(pool, processes, bpmgen.Decisions)}

	id, err := e.StartProcess(ctx, "TestMessage", nil, engine.WithBusinessKey("order-7"))
	if err != nil {
		t.Fatal(err)
	}
	mt.subscribed(id)
	if n, err := e.CorrelateMessage(ctx, "paymentReceived", "order-7", map[string]any{"paid": true}); err != nil || n != 1 {
		t.Fatalf("correlate: %d, %v", n, err)
	}
	if variables := mt.waitStatus(id, engine.InstanceCompleted); variables["paid"] != true {
		t.Errorf("variables = %v", variables)
	}
}
