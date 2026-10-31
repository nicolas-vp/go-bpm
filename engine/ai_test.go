package engine

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/bpm"
)

func TestRenderPrompt(t *testing.T) {
	variables := map[string]any{
		"request": map[string]any{"text": "Не работает вход", "id": float64(7)},
		"tags":    []any{"a", "b"},
	}
	got, err := renderPrompt("Обращение ${request.text} (${ request.id }), теги ${tags}, цена $5 {x}", variables)
	if want := `Обращение Не работает вход (7), теги ["a","b"], цена $5 {x}`; err != nil || got != want {
		t.Errorf("got %q, %v, want %q", got, err, want)
	}
	if _, err := renderPrompt("${missing} и ${request.none}", variables); err == nil || !strings.Contains(err.Error(), "missing, request.none") {
		t.Errorf("missing: err = %v", err)
	}
}

func TestParseJSONAnswer(t *testing.T) {
	for answer, want := range map[string]any{
		`{"ok": true}`:                 map[string]any{"ok": true},
		"```json\n{\"ok\": true}\n```": map[string]any{"ok": true},
		" 42 ":                         float64(42),
		"```\n[\"a\"]\n```":            []any{"a"},
	} {
		got, err := parseJSONAnswer(answer)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, %v, want %v", answer, got, err, want)
		}
	}
	if _, err := parseJSONAnswer("Конечно! Вот ответ"); err == nil {
		t.Error("text: want error")
	}
}

func TestSetVariable(t *testing.T) {
	variables := map[string]any{"a": map[string]any{"x": 1}, "s": "text"}
	if err := setVariable(variables, "a.b.c", "v"); err != nil {
		t.Fatal(err)
	}
	if err := setVariable(variables, "top", 1); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": map[string]any{"x": 1, "b": map[string]any{"c": "v"}}, "s": "text", "top": 1}
	if !reflect.DeepEqual(variables, want) {
		t.Errorf("variables = %v", variables)
	}
	if err := setVariable(variables, "s.x", 1); err == nil {
		t.Error("s is a string: want error")
	}
}

func TestAiHandler(t *testing.T) {
	var got AIRequest
	client := AIClientFunc(func(ctx context.Context, request AIRequest) (string, error) {
		got = request
		if request.JSON {
			return "```json\n{\"ok\": true}\n```", nil
		}
		return "billing", nil
	})
	expressions := map[string]*bpm.Expression{
		"text": {ElementId: "text", ActivateType: bpm.ActivateTypeQueue, TaskType: "ai", AITask: &bpm.AITask{
			Model: "m", SystemPrompt: "Категории: ${categories}", Prompt: "${request.text}", ResultVariable: "result.category"}},
		"json": {ElementId: "json", ActivateType: bpm.ActivateTypeQueue, TaskType: "ai", AITask: &bpm.AITask{
			Prompt: "Проверь", ResultVariable: "verdict", JSON: true}},
	}
	s := &Engine{deployment: map[string]bpm.DeploymentProcess{"P": {Expressions: expressions}}, ai: client, log: slog.Default()}

	job := bpm.Job{ProcessName: "P", ElementId: "text", InstanceUid: "i"}
	variables, err := s.aiHandler(context.Background(), job, map[string]any{
		"categories": []any{"billing", "login"}, "request": map[string]any{"text": "Списали дважды"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (AIRequest{Model: "m", SystemPrompt: `Категории: ["billing","login"]`, Prompt: "Списали дважды", Job: job}); !reflect.DeepEqual(got, want) {
		t.Errorf("request = %+v, want %+v", got, want)
	}
	if category := variables["result"].(map[string]any)["category"]; category != "billing" {
		t.Errorf("result.category = %v", category)
	}

	variables, err = s.aiHandler(context.Background(), bpm.Job{ProcessName: "P", ElementId: "json"}, nil)
	if err != nil || !reflect.DeepEqual(variables["verdict"], map[string]any{"ok": true}) || !got.JSON {
		t.Errorf("json: %v, %v, request %+v", variables, err, got)
	}
}

func TestAiHandlerWithoutClient(t *testing.T) {
	expressions := map[string]*bpm.Expression{"ai": {ElementId: "ai", ActivateType: bpm.ActivateTypeQueue, TaskType: "ai",
		AITask: &bpm.AITask{Prompt: "x", ResultVariable: "r"}}}
	s := &Engine{deployment: map[string]bpm.DeploymentProcess{"P": {Expressions: expressions}}}
	if _, err := s.aiHandler(context.Background(), bpm.Job{ProcessName: "P", ElementId: "ai"}, nil); err == nil || !strings.Contains(err.Error(), "Config.AI") {
		t.Errorf("err = %v", err)
	}
}
