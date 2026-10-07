// Package localapi — REST окна «Локальная модель» (v26).
//
//	GET  /api/local/status — сервер, версия, модели, что в памяти; список проверок
//	POST /api/local/run    — одна проверка {id}
//	POST /api/local/ask    — свободный вопрос {prompt, system}
//
// Запросы к модели идут по очереди: локальный сервер обычно держит одну
// модель и одну очередь, а параллельные запросы из окна только путали бы
// замер скорости.
package localapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/local"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/server"
)

// Prefix — корень маршрутов.
const Prefix = "/api/local/"

const maxPrompt = 8000

// CheckView — проверка без функций.
type CheckView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Level  string `json:"level"`
	What   string `json:"what"`
	System string `json:"system,omitempty"`
	Prompt string `json:"prompt"`
	Expect string `json:"expect"`
	Tools  bool   `json:"tools,omitempty"`
}

// StatusView — ответ GET /api/local/status.
type StatusView struct {
	local.Status
	Checks []CheckView `json:"checks"`
}

// API — обработчики. Chat — клиент модели; пусто — Config.Client().
type API struct {
	Config local.Config
	Chat   llm.Chatter
	mu     sync.Mutex
}

// Extension — маршруты окна.
func Extension(a *API) []server.Extension {
	if a.Chat == nil {
		a.Chat = a.Config.Client()
	}
	a.Config = a.Config.Normalize()
	return []server.Extension{{Prefix: Prefix, Handler: a}}
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, Prefix) {
	case "status":
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "только GET")
			return
		}
		v := StatusView{Status: local.Probe(r.Context(), a.Config, nil)}
		for _, c := range local.Checks() {
			v.Checks = append(v.Checks, CheckView{ID: c.ID, Title: c.Title, Level: c.Level, What: c.What,
				System: c.System, Prompt: c.Prompt, Expect: c.Expect, Tools: c.HasTools()})
		}
		writeJSON(w, v)
	case "run":
		var in struct {
			ID string `json:"id"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		ch, ok := local.Find(in.ID)
		if !ok {
			server.WriteError(w, http.StatusNotFound, "нет проверки "+in.ID)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		writeJSON(w, local.Run(r.Context(), a.Chat, a.Config.Model, ch))
	case "ask":
		var in struct {
			Prompt string `json:"prompt"`
			System string `json:"system"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		in.Prompt = strings.TrimSpace(in.Prompt)
		if in.Prompt == "" || len([]rune(in.Prompt)) > maxPrompt {
			server.WriteError(w, http.StatusBadRequest, "вопрос пустой или длиннее 8000 знаков")
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		writeJSON(w, local.Ask(r.Context(), a.Chat, a.Config.Model, in.System, in.Prompt, 1024))
	default:
		server.WriteError(w, http.StatusNotFound, "нет такого маршрута")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "только POST")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		server.WriteError(w, http.StatusBadRequest, "тело — не JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
