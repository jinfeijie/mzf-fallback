package main

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"
)

//go:embed templates/index.html
var templateFS embed.FS

func newApp(store *ConfigStore, guardian *Guardian) http.Handler {
	tpl := template.Must(template.ParseFS(templateFS, "templates/index.html"))
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if err := tpl.Execute(w, nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": guardian.Snapshot()})
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg, err := store.Load()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": cfg})
		case http.MethodPost:
			var cfg Config
			if err := decodeJSON(r, &cfg); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			if err := store.Save(cfg); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			loaded, err := store.Load()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": loaded})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/api/action/switch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req struct {
			ID     int64 `json:"id"`
			Status int   `json:"status"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if req.ID == 0 || (req.Status != 1 && req.Status != 2) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id 和 status 必填，status 只能是 1 或 2"})
			return
		}
		data, err := guardian.ManualSwitch(req.ID, req.Status)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	})

	mux.HandleFunc("/api/action/test-bark", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		current, err := store.Load()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		var patch Config
		if err := decodeJSON(r, &patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		mergeConfig(&current, patch)
		if err := guardian.TestBark(current); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ok": true}})
	})

	mux.HandleFunc("/api/action/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		data, err := guardian.RunOnce()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ok": true}})
	})

	return logMiddleware(mux)
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = jsonEncode(w, payload)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return jsonDecode(r.Body, v)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Truncate(time.Millisecond))
	})
}

func parsePort(defaultPort string) string {
	if v := getenv("PORT"); v != "" {
		if _, err := strconv.Atoi(v); err == nil {
			return v
		}
	}
	return defaultPort
}
