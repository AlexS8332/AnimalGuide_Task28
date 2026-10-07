package compareapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/compare"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/netaudit"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/rag"
)

type fixed struct{ text string }

func (f fixed) Chat(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Message: llm.Message{Content: f.text}, Usage: llm.Usage{Prompt: 100, Completion: 20}}, nil
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func mux(a *API) http.Handler {
	m := http.NewServeMux()
	for _, e := range Extension(a) {
		m.Handle(e.Prefix, e.Handler)
	}
	return m
}

func TestAskBothSides(t *testing.T) {
	dir := t.TempDir()
	qpath := filepath.Join(dir, "q.json")
	os.WriteFile(qpath, []byte(`{"schema":1,"version":1,"questions":[{"id":"T01","split":"test","q":"Сколько весит манул?","answerable":true,"expect":{"must":[["2"],["5"]]}},{"id":"D01","split":"dev","q":"x"}]}`), 0o644)
	a := &API{Mode: rag.NoRAG, Questions: qpath, Net: netaudit.New(nil),
		Sides: []compare.Side{
			{Name: compare.Local, Label: "локальная", Answerer: &rag.Answerer{LLM: fixed{"Манул весит от 2 до 5 кг."}, Model: "qwen"}},
			{Name: compare.Cloud, Label: "облачная", Answerer: &rag.Answerer{LLM: fixed{"Не знаю."}, Model: "deepseek"}},
		}}
	h := mux(a)

	w := do(t, h, http.MethodGet, "/api/compare/status", "")
	var st StatusView
	json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Ready || len(st.Sides) != 2 || len(st.Questions) != 1 || st.Report {
		t.Fatalf("status: %s", w.Body)
	}

	w = do(t, h, http.MethodPost, "/api/compare/ask", `{"qid":"T01"}`)
	var v AskView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body)
	}
	if v.Q != "Сколько весит манул?" || len(v.Results) != 2 || v.Results[0].Side != compare.Local {
		t.Fatalf("ask: %+v", v)
	}
	if v.Results[0].Verdict != rag.Correct || v.Results[1].Verdict == rag.Correct || v.Results[0].Model != "qwen" {
		t.Fatalf("вердикты: %+v", v.Results)
	}
	if do(t, h, http.MethodPost, "/api/compare/ask", `{"qid":"нет"}`).Code != 404 {
		t.Error("неизвестный вопрос")
	}
	if do(t, h, http.MethodPost, "/api/compare/ask", `{"q":"  "}`).Code != 400 {
		t.Error("пустой вопрос")
	}
	if do(t, h, http.MethodGet, "/api/compare/report", "").Code != 404 {
		t.Error("отчёта нет — 404")
	}
}

func TestReportAndUnavailable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	raw, _ := json.Marshal(compare.Report{Sides: []compare.SideReport{{Name: compare.Local, Report: rag.Report{Model: "тяжёлое"}}}})
	os.WriteFile(path, raw, 0o644)
	a := &API{Why: "базы знаний нет", ReportPath: path}
	h := mux(a)
	if w := do(t, h, http.MethodPost, "/api/compare/ask", `{"q":"манул"}`); w.Code != 503 || !strings.Contains(w.Body.String(), "базы") {
		t.Fatalf("без базы: %d %s", w.Code, w.Body)
	}
	w := do(t, h, http.MethodGet, "/api/compare/report", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "тяжёлое") {
		t.Fatalf("отчёт без полных прогонов: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, http.MethodGet, "/api/net", ""); w.Code != 503 {
		t.Fatalf("учёт сети выключен: %d", w.Code)
	}
}

func TestNet(t *testing.T) {
	audit := netaudit.New(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	resp, _ := (&http.Client{Transport: audit}).Get(srv.URL)
	resp.Body.Close()
	h := mux(&API{Net: audit})
	var s netaudit.Snapshot
	json.Unmarshal(do(t, h, http.MethodGet, "/api/net", "").Body.Bytes(), &s)
	if s.ByClass[netaudit.ClassLoopback] != 1 || s.Internet != 0 {
		t.Fatalf("сеть: %+v", s)
	}
	if do(t, h, http.MethodGet, "/api/net/reset", "").Code != 405 {
		t.Error("сброс — только POST")
	}
	json.Unmarshal(do(t, h, http.MethodPost, "/api/net/reset", "").Body.Bytes(), &s)
	if len(s.Hosts) != 0 {
		t.Fatalf("после сброса: %+v", s)
	}
}
