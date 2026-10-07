// Package compare — RAG локально против облака (v28): один и тот же
// конвейер (индекс базы знаний, локальный эмбеддер, поиск, фильтр,
// переписывание, ответ kb_answer с цитатами) с двумя генераторами —
// локальной моделью и облачной. Сравниваются качество, скорость и
// стабильность на контрольных вопросах.
//
// Качество — те же два голоса, что в v22–v24: правило (код) и судья. Судья
// один на обе стороны, чтобы оценка не зависела от того, кого оценивают.
// Скорость — время поиска и генерации отдельно, токены в секунду.
// Стабильность — повторы одного вопроса: смены вердикта, ошибки и
// отказы протокола, совпадение источников и текста между повторами.
package compare

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/kb"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/rag"
)

// Стороны сравнения.
const (
	Local = "local"
	Cloud = "cloud"
)

// Side — одна сторона: генератор ответа. Поиск у сторон общий —
// Answerer.Searcher и Answerer.Pipeline указывают на одну базу.
type Side struct {
	Name     string
	Label    string
	Answerer *rag.Answerer
}

// Options — что сравнивать.
type Options struct {
	Splits  []string // пусто — test и out
	Mode    rag.Mode // пусто — rag+cite
	Repeats int      // 0 → 3
	Judge   *rag.Judge
	// JudgeLabel — кто судья: «облачная · deepseek-v4-flash».
	JudgeLabel string
	Progress   func(side string, row rag.Row)
}

// Speed — скорость стороны.
type Speed struct {
	Answers   int     `json:"answers"`
	GenMeanMS int64   `json:"gen_mean_ms"`
	GenP50MS  int64   `json:"gen_p50_ms"`
	GenP95MS  int64   `json:"gen_p95_ms"`
	GenMaxMS  int64   `json:"gen_max_ms"`
	SearchMS  float64 `json:"search_mean_ms"`
	TokPerS   float64 `json:"tok_per_s"`
	Prompt    float64 `json:"prompt_mean"`
	Output    float64 `json:"output_mean"`
	CostUSD   float64 `json:"cost_usd"`
	CostKnown bool    `json:"cost_known"`
}

// Stability — стабильность стороны.
type Stability struct {
	Runs       int     `json:"runs"`
	Errors     int     `json:"errors"`     // ход не дал ответа: сбой, протокол, таймаут
	Unverified int     `json:"unverified"` // kb_answer не прошёл проверку цитат
	Flips      int     `json:"flips"`
	FlipRate   float64 `json:"flip_rate"`
	// SameSources, SameText — доля вопросов, у которых во всех повторах
	// один и тот же набор источников / дословно один и тот же текст.
	SameSources float64 `json:"same_sources"`
	SameText    float64 `json:"same_text"`
}

// SideReport — итог стороны.
type SideReport struct {
	Name      string        `json:"name"`
	Label     string        `json:"label"`
	Model     string        `json:"model"`
	Stats     rag.ModeStats `json:"stats"`
	Speed     Speed         `json:"speed"`
	Stability Stability     `json:"stability"`
	Report    rag.Report    `json:"report"`
}

// QRow — вопрос: вердикты сторон рядом.
type QRow struct {
	ID         string                 `json:"id"`
	Q          string                 `json:"q"`
	Split      string                 `json:"split"`
	Answerable bool                   `json:"answerable"`
	Verdict    map[string]rag.Verdict `json:"verdict"`
	Flips      map[string]int         `json:"flips"`
	GenMS      map[string]int64       `json:"gen_ms"`
	Answer     map[string]string      `json:"answer"`
	Sources    map[string][]string    `json:"sources"`
}

// Report — сравнение целиком.
type Report struct {
	Created    time.Time    `json:"created"`
	Mode       rag.Mode     `json:"mode"`
	Splits     []string     `json:"splits"`
	Repeats    int          `json:"repeats"`
	Index      string       `json:"index"`
	K          int          `json:"k"`
	Embedder   string       `json:"embedder"`
	Judge      string       `json:"judge"`
	Sides      []SideReport `json:"sides"`
	Rows       []QRow       `json:"rows"`
	Conclusion []string     `json:"conclusion"`
}

// Run гоняет стороны по очереди: локальный сервер держит одну модель, и
// параллельный облачный прогон не мешал бы ему, но мешал бы честному
// замеру времени поиска — эмбеддер у сторон общий.
func Run(ctx context.Context, sides []Side, qs kb.QuestionSet, o Options) (Report, error) {
	if len(sides) == 0 {
		return Report{}, errors.New("нет сторон для сравнения")
	}
	if o.Mode == "" {
		o.Mode = rag.RAGCite
	}
	if o.Repeats <= 0 {
		o.Repeats = 3
	}
	if len(o.Splits) == 0 {
		o.Splits = []string{"test", "out"}
	}
	rep := Report{Created: time.Now(), Mode: o.Mode, Splits: o.Splits, Repeats: o.Repeats, Judge: o.JudgeLabel}
	if rep.Judge == "" {
		rep.Judge = "только правило"
	}
	for _, s := range sides {
		if s.Answerer == nil || s.Answerer.LLM == nil {
			return Report{}, fmt.Errorf("сторона %s без модели", s.Name)
		}
		eo := rag.EvalOptions{Splits: o.Splits, Modes: []rag.Mode{o.Mode}, Repeats: o.Repeats, Judge: o.Judge, Seed: 28}
		if o.Progress != nil {
			name := s.Name
			eo.Progress = func(r rag.Row) { o.Progress(name, r) }
		}
		r, err := rag.Eval(ctx, s.Answerer, qs, eo)
		if err != nil {
			return Report{}, fmt.Errorf("сторона %s: %w", s.Name, err)
		}
		sr := SideReport{Name: s.Name, Label: s.Label, Model: s.Answerer.Model, Report: r,
			Speed: speedOf(r, o.Mode), Stability: stabilityOf(r, o.Mode)}
		for _, st := range r.Stats {
			if st.Mode == o.Mode {
				sr.Stats = st
			}
		}
		rep.Index, rep.K, rep.Embedder = r.Index, r.K, r.Embedder
		rep.Sides = append(rep.Sides, sr)
	}
	rep.Rows = rows(rep.Sides, o.Mode)
	rep.Conclusion = conclude(rep)
	return rep, nil
}

func speedOf(r rag.Report, m rag.Mode) Speed {
	var sp Speed
	var gen []int64
	var search, prompt, output float64
	var tokS float64
	var nTok int
	sp.CostKnown = true
	for _, row := range r.Rows {
		for _, run := range row.Runs[m] {
			if run.Error != "" {
				continue
			}
			a := run.Answer
			sp.Answers++
			gen = append(gen, a.Millis)
			search += searchMS(a)
			prompt += float64(a.Usage.Prompt)
			output += float64(a.Usage.Completion)
			if a.Millis > 0 && a.Usage.Completion > 0 {
				tokS += float64(a.Usage.Completion) / (float64(a.Millis) / 1000)
				nTok++
			}
			sp.CostUSD += a.Cost.USD
			sp.CostKnown = sp.CostKnown && a.Cost.Known
		}
	}
	if sp.Answers == 0 {
		return sp
	}
	sort.Slice(gen, func(i, j int) bool { return gen[i] < gen[j] })
	var sum int64
	for _, g := range gen {
		sum += g
	}
	n := float64(sp.Answers)
	sp.GenMeanMS = sum / int64(len(gen))
	sp.GenP50MS = gen[len(gen)/2]
	sp.GenP95MS = gen[min(len(gen)-1, int(float64(len(gen))*0.95))]
	sp.GenMaxMS = gen[len(gen)-1]
	sp.SearchMS = search / n
	sp.Prompt, sp.Output = prompt/n, output/n
	if nTok > 0 {
		sp.TokPerS = tokS / float64(nTok)
	}
	return sp
}

// searchMS — время поиска ответа: конвейер (трасса) или прямой поиск.
func searchMS(a rag.Answer) float64 {
	if a.Trace != nil && a.Trace.Millis > 0 {
		return float64(a.Trace.Millis)
	}
	return a.Search.Millis
}

func stabilityOf(r rag.Report, m rag.Mode) Stability {
	var st Stability
	var sameSrc, sameText, multi int
	for _, row := range r.Rows {
		runs := row.Runs[m]
		st.Flips += row.Flips[m]
		var srcs, texts []string
		for _, run := range runs {
			st.Runs++
			if run.Error != "" {
				st.Errors++
				continue
			}
			if c := run.Answer.Cited; c != nil && c.Check.Unverified {
				st.Unverified++
			}
			srcs = append(srcs, strings.Join(sourcesOf(run.Answer), ","))
			texts = append(texts, strings.TrimSpace(run.Answer.Text))
		}
		if len(runs) > 1 {
			multi++
			if len(srcs) == len(runs) && allSame(srcs) {
				sameSrc++
			}
			if len(texts) == len(runs) && allSame(texts) {
				sameText++
			}
		}
	}
	if st.Runs > 0 {
		pairs := st.Runs - len(r.Rows)
		if pairs > 0 {
			st.FlipRate = float64(st.Flips) / float64(pairs)
		}
	}
	if multi > 0 {
		st.SameSources = float64(sameSrc) / float64(multi)
		st.SameText = float64(sameText) / float64(multi)
	}
	return st
}

// sourcesOf — chunk_id источников ответа (rag+cite) или выдачи (rag).
func sourcesOf(a rag.Answer) []string {
	var ids []string
	if a.Cited != nil {
		for _, s := range a.Cited.Cited.Sources {
			ids = append(ids, s.ChunkID)
		}
	} else {
		for _, h := range a.Hits {
			ids = append(ids, h.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func allSame(xs []string) bool {
	for _, x := range xs[1:] {
		if x != xs[0] {
			return false
		}
	}
	return true
}

func rows(sides []SideReport, m rag.Mode) []QRow {
	byID := map[string]*QRow{}
	var order []string
	for _, s := range sides {
		for _, row := range s.Report.Rows {
			q := row.Question
			x, ok := byID[q.ID]
			if !ok {
				x = &QRow{ID: q.ID, Q: q.Q, Split: q.Split, Answerable: q.Answerable,
					Verdict: map[string]rag.Verdict{}, Flips: map[string]int{}, GenMS: map[string]int64{},
					Answer: map[string]string{}, Sources: map[string][]string{}}
				byID[q.ID] = x
				order = append(order, q.ID)
			}
			x.Verdict[s.Name] = row.Majority[m]
			x.Flips[s.Name] = row.Flips[m]
			if runs := row.Runs[m]; len(runs) > 0 {
				var sum int64
				for _, r := range runs {
					sum += r.Answer.Millis
				}
				x.GenMS[s.Name] = sum / int64(len(runs))
				first := runs[0]
				if first.Error != "" {
					x.Answer[s.Name] = "ошибка: " + first.Error
				} else {
					x.Answer[s.Name] = first.Answer.Text
					x.Sources[s.Name] = sourcesOf(first.Answer)
				}
			}
		}
	}
	out := make([]QRow, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// Side — сторона по имени.
func (r Report) Side(name string) (SideReport, bool) {
	for _, s := range r.Sides {
		if s.Name == name {
			return s, true
		}
	}
	return SideReport{}, false
}

func pct(a, b int) string {
	if b == 0 {
		return "—"
	}
	return fmt.Sprintf("%d из %d", a, b)
}

func conclude(r Report) []string {
	l, okL := r.Side(Local)
	c, okC := r.Side(Cloud)
	if !okL || !okC {
		return nil
	}
	var out []string
	ans := func(s SideReport) int { return s.Stats.Correct }
	out = append(out, fmt.Sprintf("Качество: верных ответов локально %s, в облаке %s (по большинству повторов; судья — %s).",
		pct(ans(l), l.Stats.Questions), pct(ans(c), c.Stats.Questions), r.Judge))
	out = append(out, fmt.Sprintf("«Не знаю» на вопросах вне базы: локально %d, в облаке %d; уверенных ошибок — %d и %d.",
		l.Stats.RightAbstain, c.Stats.RightAbstain, l.Stats.ConfidentWrong, c.Stats.ConfidentWrong))
	if l.Speed.GenMeanMS > 0 && c.Speed.GenMeanMS > 0 {
		out = append(out, fmt.Sprintf("Скорость: генерация в среднем %.1f с локально и %.1f с в облаке (в %.1f раза), поиск — %.0f мс у обеих сторон (общий, локальный).",
			float64(l.Speed.GenMeanMS)/1000, float64(c.Speed.GenMeanMS)/1000,
			float64(l.Speed.GenMeanMS)/float64(c.Speed.GenMeanMS), (l.Speed.SearchMS+c.Speed.SearchMS)/2))
	}
	out = append(out, fmt.Sprintf("Стабильность: смен вердикта между повторами — %d локально и %d в облаке; ошибок хода — %d и %d; ответов, не прошедших проверку цитат, — %d и %d.",
		l.Stability.Flips, c.Stability.Flips, l.Stability.Errors, c.Stability.Errors, l.Stability.Unverified, c.Stability.Unverified))
	cost := "неизвестна"
	if c.Speed.CostKnown {
		cost = fmt.Sprintf("$%.4f", c.Speed.CostUSD)
	}
	out = append(out, fmt.Sprintf("Цена генерации: локально $0, в облаке %s.", cost))
	return out
}

