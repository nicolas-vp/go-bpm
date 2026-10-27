package engine

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestWithVariables(t *testing.T) {
	instance := uuid.New()
	for name, payload := range map[string]string{
		// системная задача: токен в message, остальные поля задачи сохраняются
		"system": `{"type":"timer","boundary":"b1","message":{"InstanceUid":"` + instance.String() + `","CurrentPosition":"task","Variables":{"a":1},"Headers":{"h":"x"}}}`,
		// задача service task: токен - сам payload
		"service": `{"InstanceUid":"` + instance.String() + `","CurrentPosition":"task","Variables":{"a":1},"Headers":{"h":"x"}}`,
	} {
		patched, err := withVariables([]byte(payload), map[string]any{"a": 2, "b": "fixed"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		message, err := taskMessage(patched)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if message.InstanceUid != instance || message.CurrentPosition != "task" || message.Variables["a"] != float64(2) ||
			message.Variables["b"] != "fixed" || message.Headers["h"] != "x" {
			t.Errorf("%s: message = %+v", name, message)
		}
		if name == "system" {
			var st systemTask
			if err := json.Unmarshal(patched, &st); err != nil || st.Type != "timer" || st.Boundary != "b1" {
				t.Errorf("system task = %+v, err %v", st, err)
			}
		}
	}
	if _, err := taskMessage([]byte(`{"type":"timer"}`)); err == nil {
		t.Error("payload without token: want error")
	}
}
