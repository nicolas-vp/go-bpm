package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nicolas-vp/go-bpm/engine"
)

// fakeCorrelator: сообщение payment ждёт один токен по ключу order-1
type fakeCorrelator struct {
	ttl []bool
}

func (f *fakeCorrelator) CorrelateMessage(_ context.Context, name, key string, _ map[string]any, opts ...engine.MessageOption) (int, error) {
	var options engine.MessageOptions
	for _, opt := range opts {
		opt(&options)
	}
	f.ttl = append(f.ttl, options.TTL > 0)
	switch {
	case name == "" || key == "":
		return 0, engine.ErrMessageRequired
	case name == "payment" && key == "order-1":
		return 1, nil
	case options.TTL > 0:
		return 0, nil
	}
	return 0, fmt.Errorf("message %q: %w", name, engine.ErrMessageNotCorrelated)
}

func TestMessagesAPI(t *testing.T) {
	fake := &fakeCorrelator{}
	mux := http.NewServeMux()
	RegisterMessages(mux, fake)

	for _, tc := range []struct {
		body, wantBody string
		wantStatus     int
	}{
		{`{"name":"payment","correlationKey":"order-1","variables":{"paid":true}}`, `{"correlated":1}`, http.StatusOK},
		{`{"name":"payment","correlationKey":"order-2","ttl":"10m"}`, `{"correlated":0}`, http.StatusOK},
		{`{"name":"payment","correlationKey":"order-2"}`, "no subscription", http.StatusNotFound},
		{`{"name":"payment"}`, "required", http.StatusBadRequest},
		{`{"name":"payment","correlationKey":"order-1","ttl":"soon"}`, "ttl", http.StatusBadRequest},
		{`{`, "body", http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/messages", strings.NewReader(tc.body)))
		if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s: %d %.200q, want %d containing %q", tc.body, rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
		}
	}
	if want := fmt.Sprint([]bool{false, true, false, false}); fmt.Sprint(fake.ttl) != want {
		t.Errorf("ttl passed = %v, want %v", fake.ttl, want)
	}
}
