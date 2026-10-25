// Package historyview - страница /history/: диаграмма процесса (bpmn-js) с пройденными элементами экземпляра
// и таблицы решений DMN (dmn-js) со сработавшими правилами задач бизнес-правил
package historyview

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
)

// static - страница, bpmn-js и dmn-js (vendor/<библиотека>-<версия>, лицензия bpmn.io в LICENSE рядом)
//
//go:embed static
var static embed.FS

type DiagramService interface {
	GetDiagram(ctx context.Context, instanceUid uuid.UUID) (*engine.Diagram, error)
	GetRulesHistory(ctx context.Context, instanceUid uuid.UUID, elementId string) (*engine.RulesHistory, error)
}

// Register вешает на mux:
//   - /history/?instanceId=<id> - страница с диаграммой;
//   - /history/api/diagram?instanceId=<id> - BPMN процесса и история экземпляра в JSON;
//   - /history/api/rules?instanceId=<id>&elementId=<id> - вызовы решений задачей бизнес-правила и их DMN в JSON;
//   - /history/static/ - скрипты и стили страницы.
func Register(mux *http.ServeMux, service DiagramService) {
	// относительные ссылки страницы считаются от /history/, как у /docs/
	mux.HandleFunc("GET /history", func(w http.ResponseWriter, r *http.Request) {
		target := "/history/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /history/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, static, "static/index.html")
	})
	mux.HandleFunc("GET /history/api/diagram", diagramHandler(service))
	mux.HandleFunc("GET /history/api/rules", rulesHandler(service))
	mux.Handle("GET /history/static/", cacheControl(http.StripPrefix("/history/", http.FileServerFS(static))))
}

// cacheControl - у файлов из embed нет даты изменения, поэтому без заголовков браузер держит старые
// app.js и style.css после обновления сервиса. Страница и её скрипты перезапрашиваются всегда,
// библиотеки в vendor/ лежат в папках с версией и кэшируются надолго
func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func diagramHandler(service DiagramService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.URL.Query().Get("instanceId"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "instanceId: "+err.Error())
			return
		}
		diagram, err := service.GetDiagram(r.Context(), instanceUid)
		if errors.Is(err, engine.ErrInstanceNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "history diagram", "instance", instanceUid, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, diagram)
	}
}

func rulesHandler(service DiagramService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceUid, err := uuid.Parse(r.URL.Query().Get("instanceId"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "instanceId: "+err.Error())
			return
		}
		elementId := r.URL.Query().Get("elementId")
		if elementId == "" {
			writeError(w, http.StatusBadRequest, "elementId is required")
			return
		}
		history, err := service.GetRulesHistory(r.Context(), instanceUid, elementId)
		if err != nil {
			slog.ErrorContext(r.Context(), "rules history", "instance", instanceUid, "element", elementId, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, history)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	// BPMN - XML, без экранирования < и > ответ короче и читается в devtools
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(body)
}
