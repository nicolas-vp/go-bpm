package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegister(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, fakeDiagrams{})
	for _, tc := range []struct {
		path, wantBody string
		wantStatus     int
	}{
		{path: "/ui", wantStatus: http.StatusMovedPermanently},
		{path: "/ui/", wantStatus: http.StatusOK, wantBody: "/ui/static/app.js"},
		{path: "/ui/static/app.js", wantStatus: http.StatusOK, wantBody: "/api/v1"},
		{path: "/ui/static/style.css", wantStatus: http.StatusOK},
		{path: "/ui/static/missing.js", wantStatus: http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s: %d %.100q, want %d containing %q", tc.path, rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
		}
		if tc.wantStatus == http.StatusOK && rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", tc.path, rec.Header().Get("Cache-Control"))
		}
	}
}
