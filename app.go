package main

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

func newApp(store *ConfigStore, guardian *Guardian) http.Handler {
	tpl := template.Must(template.ParseFS(templateFS, "templates/index.html", "templates/login.html"))
	auth := NewAuthManager()
	mux := http.NewServeMux()
	apiMux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if !auth.IsAuthenticated(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if err := tpl.ExecuteTemplate(w, "index.html", nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}
	})

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if auth.IsAuthenticated(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if r.Method == http.MethodGet {
			if err := tpl.ExecuteTemplate(w, "login.html", map[string]any{"error": ""}); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}

		cfg, err := store.Load()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		username := strings.TrimSpace(r.FormValue("username"))
		password := strings.TrimSpace(r.FormValue("password"))
		if username != cfg.LoginUsername || password != cfg.LoginPassword {
			w.WriteHeader(http.StatusUnauthorized)
			if err := tpl.ExecuteTemplate(w, "login.html", map[string]any{"error": "账号或密码错误"}); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			}
			return
		}

		if err := auth.Login(w, r); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})

	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		auth.Logout(w, r)
		http.Redirect(w, r, "/login", http.StatusFound)
	})

	apiMux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": guardian.Snapshot()})
	})

	apiMux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
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

	apiMux.HandleFunc("/api/action/switch", func(w http.ResponseWriter, r *http.Request) {
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

	apiMux.HandleFunc("/api/action/test-bark", func(w http.ResponseWriter, r *http.Request) {
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

	apiMux.HandleFunc("/api/action/reload", func(w http.ResponseWriter, r *http.Request) {
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

	mux.Handle("/api/", requireAPIAuth(auth, apiMux))

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

func requireAPIAuth(auth *AuthManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.IsAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	})
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
