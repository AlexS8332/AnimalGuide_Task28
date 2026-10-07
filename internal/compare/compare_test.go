package compare

import (
	"strings"
	"testing"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/kb"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/rag"
)

func run(text string, ms int64, out int, src ...string) rag.Run {
	c := &rag.CitedResult{}
	for _, s := range src {
		c.Cited.Sources = append(c.Cited.Sources, rag.CitedSource{ChunkID: s})
	}
	return rag.Run{Answer: rag.Answer{Text: text, Millis: ms, Usage: llm.Usage{Prompt: 2000, Completion: out},
		Search: kb.SearchInfo{Millis: 30}, Cited: c, Cost: llm.Cost{Known: true, USD: 0.001}}}
}

func side(name string, rows ...rag.Row) SideReport {
	r := rag.Report{Rows: rows}
	return SideReport{Name: name, Label: name, Report: r, Speed: speedOf(r, rag.RAGCite), Stability: stabilityOf(r, rag.RAGCite),
		Stats: rag.ModeStats{Mode: rag.RAGCite, Questions: len(rows), Correct: 1}}
}

func TestSpeedAndStability(t *testing.T) {
	m := rag.RAGCite
	q1 := rag.Row{Question: kb.Question{ID: "T01", Q: "масса манула?", Split: "test", Answerable: true},
		Runs:     map[rag.Mode][]rag.Run{m: {run("2–5 кг", 10000, 100, "manul/structure/004"), run("2–5 кг", 12000, 100, "manul/structure/004"), run("2-5 кг", 20000, 100, "manul/structure/004")}},
		Majority: map[rag.Mode]rag.Verdict{m: rag.Correct}, Flips: map[rag.Mode]int{m: 0}}
	bad := run("", 0, 0)
	bad.Error = "модель (rag+cite): kb_answer не принят"
	q2 := rag.Row{Question: kb.Question{ID: "O01", Q: "ягуар?", Split: "out"},
		Runs:     map[rag.Mode][]rag.Run{m: {run("не знаю", 5000, 50), bad, run("не знаю", 6000, 50, "x/structure/001")}},
		Majority: map[rag.Mode]rag.Verdict{m: rag.Abstain}, Flips: map[rag.Mode]int{m: 1}}
	s := side(Local, q1, q2)
	if s.Speed.Answers != 5 || s.Speed.GenP50MS != 10000 || s.Speed.GenMaxMS != 20000 || s.Speed.SearchMS != 30 {
		t.Fatalf("скорость: %+v", s.Speed)
	}
	if s.Speed.TokPerS <= 0 || !s.Speed.CostKnown {
		t.Fatalf("ток/с и цена: %+v", s.Speed)
	}
	st := s.Stability
	if st.Runs != 6 || st.Errors != 1 || st.Flips != 1 || st.SameSources != 0.5 || st.SameText != 0 {
		t.Fatalf("стабильность: %+v", st)
	}
	if st.FlipRate != 0.25 { // 1 смена на 4 пары соседних повторов
		t.Fatalf("доля смен: %v", st.FlipRate)
	}

	c := side(Cloud, q1)
	rep := Report{Mode: m, Splits: []string{"test", "out"}, Repeats: 3, Judge: "облачная", Sides: []SideReport{s, c}}
	rep.Rows = rows(rep.Sides, m)
	rep.Conclusion = conclude(rep)
	if len(rep.Rows) != 2 || rep.Rows[0].Verdict[Local] != rag.Correct || rep.Rows[0].Verdict[Cloud] != rag.Correct {
		t.Fatalf("строки: %+v", rep.Rows)
	}
	if !strings.HasPrefix(rep.Rows[1].Answer[Local], "не знаю") || rep.Rows[1].GenMS[Local] == 0 {
		t.Fatalf("строка вне базы: %+v", rep.Rows[1])
	}
	if len(rep.Conclusion) < 4 || !strings.Contains(strings.Join(rep.Conclusion, " "), "Скорость") {
		t.Fatalf("вывод: %v", rep.Conclusion)
	}
	md := rep.Markdown()
	for _, want := range []string{"## Качество", "## Скорость", "## Стабильность", "| T01 | test |", "не знаю (1)", "manul/structure/004"} {
		if !strings.Contains(md, want) {
			t.Errorf("в отчёте нет %q", want)
		}
	}
}

func TestRunValidates(t *testing.T) {
	if _, err := Run(t.Context(), nil, kb.QuestionSet{}, Options{}); err == nil {
		t.Fatal("без сторон")
	}
	if _, err := Run(t.Context(), []Side{{Name: Local, Answerer: &rag.Answerer{}}}, kb.QuestionSet{}, Options{}); err == nil {
		t.Fatal("сторона без модели")
	}
}
