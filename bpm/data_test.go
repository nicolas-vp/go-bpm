package bpm

import (
	"context"
	"errors"
	"testing"
)

type testServices struct{ prefix string }

type testClient struct {
	Account struct {
		Number string `json:"number"`
	} `json:"account"`
}

type testData struct {
	Name   string      `json:"name"`
	Count  int         `json:"count"`
	Client *testClient `json:"client"`
}

func TestMethod(t *testing.T) {
	method := Method(&testServices{prefix: "hello "}, func(ctx context.Context, services *testServices, data *testData) error {
		data.Name = services.prefix + data.Name
		data.Count++
		data.Client.Account.Number += "-1"
		return nil
	})
	// count после JSON приходит float64, other нет в модели - остаётся как был
	variables := map[string]any{"name": "world", "count": float64(1), "other": true,
		"client": map[string]any{"account": map[string]any{"number": "42"}}}
	result, err := method(context.Background(), variables)
	if err != nil {
		t.Fatal(err)
	}
	if result["name"] != "hello world" || result["count"] != float64(2) || result["other"] != true {
		t.Errorf("result = %v", result)
	}
	if number := result["client"].(map[string]any)["account"].(map[string]any)["number"]; number != "42-1" {
		t.Errorf("client.account.number = %v", number)
	}
	if variables["count"] != float64(1) {
		t.Errorf("исходные переменные изменены: %v", variables)
	}
}

func TestMethodErrors(t *testing.T) {
	failure := NewError("FAIL", "fail")
	method := Method(&testServices{}, func(ctx context.Context, services *testServices, data *testData) error { return failure })

	if _, err := method(context.Background(), map[string]any{"count": "x"}); err == nil {
		t.Error("переменная не того типа: ошибки нет")
	}
	if _, err := method(context.Background(), nil); !errors.Is(err, failure) {
		t.Errorf("ошибка метода: err = %v", err)
	}
}

func TestHandler(t *testing.T) {
	handler := Handler(&testServices{}, func(ctx context.Context, services *testServices, job Job, data *testData) error {
		data.Name = job.ElementId
		return nil
	})
	result, err := handler(context.Background(), Job{ElementId: "task"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["name"] != "task" {
		t.Errorf("name = %v", result["name"])
	}
}

func TestCondition(t *testing.T) {
	condition := Condition(func(data *testData) string {
		if data.Count > 1 {
			return "many"
		}
		return "default"
	})
	if got, err := condition(map[string]any{"count": float64(2)}); err != nil || got != "many" {
		t.Errorf("count 2: %q, %v", got, err)
	}
	if got, err := condition(nil); err != nil || got != "default" {
		t.Errorf("без переменных: %q, %v", got, err)
	}
	if _, err := condition(map[string]any{"count": "x"}); err == nil {
		t.Error("переменная не того типа: ошибки нет")
	}
}

func TestDeref(t *testing.T) {
	n := 5
	if Deref(&n) != 5 || Deref[int](nil) != 0 {
		t.Error("Deref")
	}
}
