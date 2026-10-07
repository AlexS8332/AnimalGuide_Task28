// Package compareapi — REST вкладки «Локально и облако» окна «База
// знаний» (v28) и учёт сети.
//
//	GET  /api/compare/status — стороны, вопросы, есть ли отчёт kb compare
//	POST /api/compare/ask    — {q, qid}: один вопрос всем сторонам сразу
//	GET  /api/compare/report — последний отчёт kb compare (JSON)
//	GET  /api/net            — куда процесс ходил по сети
//	POST /api/net/reset      — обнулить счётчики
//
// Поиск у сторон общий (одна база, один эмбеддер), различается генератор.
// Облачная сторона есть, только если приложение запущено с -compare
// cloud: без флага локальный режим облака не касается вовсе.
package compareapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/compare"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/kb"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/netaudit"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/rag"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/server"
)

// Prefix — корень маршрутов сравнения; NetPath — учёт сети.
const (
	Prefix  = "/api/compare/"
	NetPath = "/api/net"
)

const askTimeout = 5 * time.Minute

// API — обработчики.
type API struct {
	// Sides — стороны по порядку: локальная первой. Answerer у сторон
	// разделяет Searcher и Pipeline.
	Sides []compare.Side
	// Why — почему сравнивать нечем (базы нет); пусто — можно.
	Why string
	// Questions — путь к контрольным вопросам; ReportPath — к report.json.
	Questions  string
	ReportPath string
	Mode       rag.Mode // пусто — rag+cite
	Net        *netaudit.Audit
	// Rule — оценка правилом (подменяется в тестах); nil — rag.Rule.
	Rule func(q kb.Question, answer string) rag.RuleResult
}

// Extension — маршруты.
func Extension(a *API) []server.Extension {
	return []server.Extension{
		{Prefix: Prefix, Handler: http.HandlerFunc(a.handle)},
		{Prefix: NetPath, Handler: http.HandlerFunc(a.net)},
		{Prefix: NetPath + "/", Handler: http.HandlerFunc(a.net)},
	}
}

// SideView — сторона для интерфейса.
type SideView struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Model string `json:"model"`
}

// StatusView — ответ status.
type StatusView struct {
	Sides     []SideView     `json:"sides"`
	Ready     bool           `json:"ready"`
	Why       string         `json:"why,omitempty"`
	Mode      rag.Mode       `json:"mode"`
	Questions []QuestionView `json:"questions"`
	Report    bool           `json:"report"`
}

// QuestionView — контрольный вопрос для выбора.
type QuestionView struct {
	ID         string `json:"id"`
	Split      string `json:"split"`
	Q          string `json:"q"`
	Answerable bool   `json:"answerable"`
}

// Result — ответ одной стороны.
type Result struct {
	Side     string           `json:"side"`
	Label    string           `json:"label"`
	Model    string           `json:"model"`
	Text     string           `json:"text"`
	Cited    *rag.CitedResult `json:"cited,omitempty"`
	Sources  []SourceView     `json:"sources"`
	Unknown  bool             `json:"unknown"`
	GenMS    int64            `json:"genMs"`
	SearchMS float64          `json:"searchMs"`
	Prompt   int              `json:"prompt"`
	Output   int              `json:"output"`
	TokPerS  float64          `json:"tokPerS"`
	CostUSD  float64          `json:"costUsd"`
	Error    string           `json:"error,omitempty"`
	Verdict  rag.Verdict      `json:"verdict,omitempty"`
	Why      string           `json:"why,omitempty"`
}

// SourceView — источник ответа.
type SourceView struct {
	ChunkID string `json:"chunk_id"`
	Title   string `json:"title"`
	Section string `json:"section"`
}

// AskView — ответ ask.
type AskView struct {
	Q        string   `json:"q"`
	QID      string   `json:"qid,omitempty"`
	Results  []Result `json:"results"`
	TookMS   int64    `json:"tookMs"`
	Internet int      `json:"internetBefore"` // интернет-запросов процесса до вопроса
}

func (a *API) mode() rag.Mode {
	if a.Mode == "" {
		return rag.RAGCite
	}
	return a.Mode
}

func (a *API) handle(w http.ResponseWriter, r *http.Request) {
	switch strings.Trim(strings.TrimPrefix(r.URL.Path, Prefix), "/") {
	case "status":
		a.status(w)
	case "ask":
		a.ask(w, r)
	case "report":
		a.report(w)
	default:
		server.WriteError(w, http.StatusNotFound, "нет такого маршрута")
	}
}

func (a *API) questions() []kb.Question {
	if a.Questions == "" {
		return nil
	}
	qs, err := kb.LoadQuestions(a.Questions)
	if err != nil {
		return nil
	}
	return qs.Questions
}

func (a *API) status(w http.ResponseWriter) {
	v := StatusView{Sides: []SideView{}, Ready: a.Why == "" && len(a.Sides) > 0, Why: a.Why, Mode: a.mode(), Questions: []QuestionView{}}
	for _, s := range a.Sides {
		v.Sides = append(v.Sides, SideView{Name: s.Name, Label: s.Label, Model: s.Answerer.Model})
	}
	for _, q := range a.questions() {
		if q.Split == kb.SplitTest || q.Split == "out" {
			v.Questions = append(v.Questions, QuestionView{ID: q.ID, Split: q.Split, Q: q.Q, Answerable: q.Answerable})
		}
	}
	if _, err := os.Stat(a.ReportPath); err == nil && a.ReportPath != "" {
		v.Report = true
	}
	server.WriteJSON(w, http.StatusOK, v)
}

func (a *API) ask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "только POST")
		return
	}
	if a.Why != "" || len(a.Sides) == 0 {
		server.WriteError(w, http.StatusServiceUnavailable, orStr(a.Why, "сравнивать нечем: нет отвечающего агента"))
		return
	}
	var in struct {
		Q   string `json:"q"`
		QID string `json:"qid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		server.WriteError(w, http.StatusBadRequest, "тело — не JSON: "+err.Error())
		return
	}
	var gold *kb.Question
	if in.QID != "" {
		for _, q := range a.questions() {
			if q.ID == in.QID {
				q := q
				gold = &q
				if strings.TrimSpace(in.Q) == "" {
					in.Q = q.Q
				}
			}
		}
		if gold == nil {
			server.WriteError(w, http.StatusNotFound, "нет вопроса "+in.QID)
			return
		}
	}
	in.Q = strings.TrimSpace(in.Q)
	if in.Q == "" || len([]rune(in.Q)) > 2000 {
		server.WriteError(w, http.StatusBadRequest, "вопрос пустой или длиннее 2000 знаков")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), askTimeout)
	defer cancel()
	v := AskView{Q: in.Q, QID: in.QID, Results: make([]Result, len(a.Sides))}
	if a.Net != nil {
		v.Internet = a.Net.Snapshot().Internet
	}
	started := time.Now()
	// Стороны — параллельно: локальный сервер и облако друг другу не
	// мешают, а ждать сумму двух ответов незачем.
	var wg sync.WaitGroup
	for i, s := range a.Sides {
		wg.Add(1)
		go func(i int, s compare.Side) {
			defer wg.Done()
			v.Results[i] = a.one(ctx, s, in.Q, gold)
		}(i, s)
	}
	wg.Wait()
	v.TookMS = time.Since(started).Milliseconds()
	server.WriteJSON(w, http.StatusOK, v)
}

func (a *API) one(ctx context.Context, s compare.Side, q string, gold *kb.Question) Result {
	res := Result{Side: s.Name, Label: s.Label, Model: s.Answerer.Model, Sources: []SourceView{}}
	ans, err := s.Answerer.Answer(ctx, rag.Question{Text: q}, a.mode())
	res.GenMS, res.Prompt, res.Output, res.CostUSD = ans.Millis, ans.Usage.Prompt, ans.Usage.Completion, ans.Cost.USD
	res.SearchMS = ans.Search.Millis
	if ans.Trace != nil && ans.Trace.Millis > 0 {
		res.SearchMS = float64(ans.Trace.Millis)
	}
	if ans.Millis > 0 && ans.Usage.Completion > 0 {
		res.TokPerS = float64(ans.Usage.Completion) / (float64(ans.Millis) / 1000)
	}
	if err != nil {
		res.Error = err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			res.Error = "не уложилась в " + askTimeout.String()
		}
		return res
	}
	res.Text, res.Cited = ans.Text, ans.Cited
	byID := map[string]kb.Hit{}
	for _, h := range ans.Hits {
		byID[h.ID] = h
	}
	if ans.Cited != nil {
		res.Unknown = ans.Cited.Cited.Unknown()
		for _, src := range ans.Cited.Cited.Sources {
			h := byID[src.ChunkID]
			res.Sources = append(res.Sources, SourceView{ChunkID: src.ChunkID, Title: h.Title, Section: h.Section})
		}
	}
	if gold != nil {
		rule := a.Rule
		if rule == nil {
			rule = rag.Rule
		}
		rr := rule(*gold, rag.GradedText(ans))
		res.Verdict, res.Why = rr.Verdict, ruleWhy(rr)
	}
	return res
}

func (a *API) report(w http.ResponseWriter) {
	raw, err := os.ReadFile(a.ReportPath)
	if err != nil || a.ReportPath == "" {
		server.WriteError(w, http.StatusNotFound, "отчёта нет: go run ./cmd/kb compare")
		return
	}
	var rep compare.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "отчёт не разобрался: "+err.Error())
		return
	}
	// Полные отчёты сторон (каждый прогон с промптами) окну не нужны.
	for i := range rep.Sides {
		rep.Sides[i].Report = rag.Report{}
	}
	server.WriteJSON(w, http.StatusOK, rep)
}

func (a *API) net(w http.ResponseWriter, r *http.Request) {
	if a.Net == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "учёт сети выключен")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/reset") {
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "только POST")
			return
		}
		a.Net.Reset()
	}
	server.WriteJSON(w, http.StatusOK, a.Net.Snapshot())
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ruleWhy — почему правило так решило: чего не нашлось и что лишнее.
func ruleWhy(r rag.RuleResult) string {
	var parts []string
	if len(r.Hit) > 0 {
		parts = append(parts, "есть: "+strings.Join(r.Hit, ", "))
	}
	if len(r.Miss) > 0 {
		parts = append(parts, "нет: "+strings.Join(r.Miss, ", "))
	}
	if len(r.Bad) > 0 {
		parts = append(parts, "лишнее: "+strings.Join(r.Bad, ", "))
	}
	if r.Note != "" {
		parts = append(parts, r.Note)
	}
	return strings.Join(parts, "; ")
}
