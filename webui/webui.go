// Package webui - консоль /ui/: процессы со схемами и счётчиками, запуск процесса с переменными, поиск экземпляров
// со страницами, задачи пользователей (очередь, взять, вернуть, выполнить) и история экземпляра /ui/history/
// с диаграммой bpmn-js и сработавшими правилами DMN (dmn-js).
//
// Консоль работает поверх JSON API httpapi (Register и RegisterUserTasks), поэтому они регистрируются на том же mux
package webui

import (
	"embed"
	"net/http"
	"strings"
)

// static - страницы, их скрипты и стили; bpmn-js и dmn-js в vendor/<библиотека>-<версия>, лицензия bpmn.io в LICENSE рядом
//
//go:embed static
var static embed.FS

// Register вешает на mux:
//   - /ui/ - консоль, вкладки переключаются в адресе после #;
//   - /ui/history/?instanceId=<id> - история экземпляра;
//   - /ui/history/api/diagram?instanceId=<id> - BPMN процесса и история экземпляра в JSON;
//   - /ui/history/api/rules?instanceId=<id>&elementId=<id> - вызовы решений задачей бизнес-правила и их DMN в JSON;
//   - /ui/history/api/tasks?instanceId=<id>&elementId=<id> - задачи пользователя элемента: назначение и выполнение;
//   - /ui/static/ - скрипты, стили и библиотеки страниц;
//   - /history/ - прежний адрес истории, перенаправляет на /ui/history/ с теми же параметрами.
func Register(mux *http.ServeMux, history DiagramService) {
	// относительные ссылки страниц считаются от папки: /ui/ и /ui/history/
	mux.HandleFunc("GET /ui", redirectTo("/ui/"))
	mux.HandleFunc("GET /ui/{$}", page("static/index.html"))
	mux.HandleFunc("GET /ui/history", redirectTo("/ui/history/"))
	mux.HandleFunc("GET /ui/history/{$}", page("static/history.html"))
	mux.HandleFunc("GET /ui/history/api/diagram", diagramHandler(history))
	mux.HandleFunc("GET /ui/history/api/rules", rulesHandler(history))
	mux.HandleFunc("GET /ui/history/api/tasks", userTasksHandler(history))
	mux.Handle("GET /ui/static/", cacheControl(http.StripPrefix("/ui/", http.FileServerFS(static))))
	mux.HandleFunc("GET /history", redirectTo("/ui/history/"))
	mux.HandleFunc("GET /history/", redirectTo("/ui/history/"))
}

// redirectTo перенаправляет на target с параметрами запроса: ссылки на историю ведут с ?instanceId=
func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		location := target
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, location, http.StatusMovedPermanently)
	}
}

func page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, static, name)
	}
}

// cacheControl - у файлов из embed нет даты изменения, поэтому без заголовков браузер держит старые
// скрипты и стили после обновления сервиса. Они перезапрашиваются всегда, библиотеки в vendor/ лежат
// в папках с версией и кэшируются надолго
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
