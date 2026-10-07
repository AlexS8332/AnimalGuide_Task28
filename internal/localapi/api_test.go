package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/local"
)

type echo struct{ got []llm.Request }

func (e *echo) Chat(_ context.Context, r llm.Request) (llm.Response, error) {
	e.got = append(e.got, r)
	return llm.Response{Message: llm.Message{Content: "Ursus arctos"}, Usage: llm.Usage{Prompt: 3, Completion: 2}}, nil
}

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestRoutes(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	chat := &echo{}
	ext := Extension(&API{Config: local.Config{BaseURL: down.URL, Model: "m"}, Chat: chat})
	h := ext[0].Handler

	w := call(t, h, http.MethodGet, "/api/local/status", "")
	var st StatusView
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	if st.OK || len(st.Checks) < 3 || st.Checks[0].Prompt == "" {
		t.Fatalf("status: %+v", st)
	}

	w = call(t, h, http.MethodPost, "/api/local/run", `{"id":"fact"}`)
	var r local.Result
	json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 200 || r.OK == nil || !*r.OK || chat.got[0].Model != "m" {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, http.MethodPost, "/api/local/run", `{"id":"nope"}`); w.Code != 404 {
		t.Errorf("неизвестная проверка: %d", w.Code)
	}
	w = call(t, h, http.MethodPost, "/api/local/ask", `{"prompt":"Чем питается рысь?"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Ursus") {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, http.MethodPost, "/api/local/ask", `{"prompt":"  "}`); w.Code != 400 {
		t.Errorf("пустой вопрос: %d", w.Code)
	}
	if w := call(t, h, http.MethodGet, "/api/local/run", ""); w.Code != 405 {
		t.Errorf("GET run: %d", w.Code)
	}
}
