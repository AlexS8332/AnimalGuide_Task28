package compare

import (
	"fmt"
	"strings"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/rag"
)

var verdictRu = map[rag.Verdict]string{
	rag.Correct: "верно", rag.Partial: "частично", rag.Wrong: "неверно", rag.Abstain: "не знаю",
}

func vRu(v rag.Verdict) string {
	if s, ok := verdictRu[v]; ok {
		return s
	}
	if v == "" {
		return "—"
	}
	return string(v)
}

func sec(ms int64) string { return fmt.Sprintf("%.1f с", float64(ms)/1000) }
func pc(f float64) string { return fmt.Sprintf("%.0f %%", 100*f) }
func cellMD(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "|", "\\|"), "\n", " ")
}

// Markdown — отчёт сравнения.
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# RAG локально против облака\n\n")
	fmt.Fprintf(&b, "Прогон %s. Команда: `go run ./cmd/kb compare -runs %d`.\n\n", r.Created.Format("2006-01-02 15:04"), r.Repeats)
	b.WriteString("Конвейер у сторон общий и локальный: база `kb.db` (индекс недели 6), эмбеддер, поиск, фильтр и переписывание запроса — на этой машине. Различается только генератор ответа.\n\n")
	fmt.Fprintf(&b, "| | |\n|---|---|\n| режим | `%s` — ответ kb_answer с источниками и цитатами, «не знаю» по порогу |\n", r.Mode)
	fmt.Fprintf(&b, "| индекс | %s, k=%d |\n| эмбеддер | %s |\n| вопросы | %s, повторов %d |\n| судья | %s |\n",
		r.Index, r.K, r.Embedder, strings.Join(r.Splits, " + "), r.Repeats, r.Judge)
	for _, s := range r.Sides {
		fmt.Fprintf(&b, "| %s | %s |\n", sideRu(s.Name), s.Label)
	}

	b.WriteString("\n## Итог\n\n")
	for _, l := range r.Conclusion {
		b.WriteString("- " + l + "\n")
	}

	b.WriteString("\n## Качество\n\n| | " + header(r) + "\n|---|" + strings.Repeat("---|", len(r.Sides)) + "\n")
	row := func(name string, f func(SideReport) string) {
		fmt.Fprintf(&b, "| %s |", name)
		for _, s := range r.Sides {
			fmt.Fprintf(&b, " %s |", f(s))
		}
		b.WriteString("\n")
	}
	row("вопросов", func(s SideReport) string { return fmt.Sprint(s.Stats.Questions) })
	row("верно / частично / неверно / не знаю", func(s SideReport) string {
		return fmt.Sprintf("%d / %d / %d / %d", s.Stats.Correct, s.Stats.Partial, s.Stats.Wrong, s.Stats.Abstain)
	})
	row("уверенные ошибки", func(s SideReport) string { return fmt.Sprint(s.Stats.ConfidentWrong) })
	row("«не знаю» вне базы", func(s SideReport) string { return fmt.Sprint(s.Stats.RightAbstain) })
	row("ответ по существу вне базы", func(s SideReport) string { return fmt.Sprint(s.Stats.AnsweredUnanswerable) })
	row("нужный фрагмент в выдаче (recall)", func(s SideReport) string { return pc(s.Stats.Recall) })
	row("согласие правила и судьи", func(s SideReport) string {
		if s.Stats.Judged == 0 {
			return "—"
		}
		return pc(s.Stats.Agreement)
	})

	b.WriteString("\n## Скорость\n\n| | " + header(r) + "\n|---|" + strings.Repeat("---|", len(r.Sides)) + "\n")
	row("ответов", func(s SideReport) string { return fmt.Sprint(s.Speed.Answers) })
	row("генерация: среднее / медиана / p95 / максимум", func(s SideReport) string {
		return fmt.Sprintf("%s / %s / %s / %s", sec(s.Speed.GenMeanMS), sec(s.Speed.GenP50MS), sec(s.Speed.GenP95MS), sec(s.Speed.GenMaxMS))
	})
	row("поиск (локальный), среднее", func(s SideReport) string { return fmt.Sprintf("%.0f мс", s.Speed.SearchMS) })
	row("токенов ответа в секунду", func(s SideReport) string { return fmt.Sprintf("%.1f", s.Speed.TokPerS) })
	row("токенов на ответ: запрос → ответ", func(s SideReport) string {
		return fmt.Sprintf("%.0f → %.0f", s.Speed.Prompt, s.Speed.Output)
	})
	row("цена генерации", func(s SideReport) string {
		if !s.Speed.CostKnown {
			return "?"
		}
		return fmt.Sprintf("$%.4f", s.Speed.CostUSD)
	})

	b.WriteString("\n## Стабильность\n\n| | " + header(r) + "\n|---|" + strings.Repeat("---|", len(r.Sides)) + "\n")
	row("прогонов", func(s SideReport) string { return fmt.Sprint(s.Stability.Runs) })
	row("ошибок хода (сбой, протокол, таймаут)", func(s SideReport) string { return fmt.Sprint(s.Stability.Errors) })
	row("ответов, не прошедших проверку цитат", func(s SideReport) string { return fmt.Sprint(s.Stability.Unverified) })
	row("смен вердикта между повторами", func(s SideReport) string {
		return fmt.Sprintf("%d (%s пар)", s.Stability.Flips, pc(s.Stability.FlipRate))
	})
	row("те же источники во всех повторах", func(s SideReport) string { return pc(s.Stability.SameSources) })
	row("дословно тот же текст во всех повторах", func(s SideReport) string { return pc(s.Stability.SameText) })

	b.WriteString("\n## По вопросам\n\nВердикт — большинство повторов; в скобках — смен вердикта; время — средняя генерация.\n\n")
	b.WriteString("| id | набор | вопрос |")
	for _, s := range r.Sides {
		fmt.Fprintf(&b, " %s |", sideRu(s.Name))
	}
	b.WriteString("\n|---|---|---|" + strings.Repeat("---|", len(r.Sides)) + "\n")
	for _, q := range r.Rows {
		fmt.Fprintf(&b, "| %s | %s | %s |", q.ID, q.Split, cellMD(q.Q))
		for _, s := range r.Sides {
			flip := ""
			if q.Flips[s.Name] > 0 {
				flip = fmt.Sprintf(" (%d)", q.Flips[s.Name])
			}
			fmt.Fprintf(&b, " %s%s · %s |", vRu(q.Verdict[s.Name]), flip, sec(q.GenMS[s.Name]))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Ответы рядом (первый повтор)\n")
	for _, q := range r.Rows {
		fmt.Fprintf(&b, "\n### %s. %s\n", q.ID, q.Q)
		for _, s := range r.Sides {
			fmt.Fprintf(&b, "\n**%s** — %s, источники: %s\n\n", sideRu(s.Name), vRu(q.Verdict[s.Name]), orDash(strings.Join(q.Sources[s.Name], ", ")))
			for _, line := range strings.Split(strings.TrimSpace(q.Answer[s.Name]), "\n") {
				b.WriteString("> " + line + "\n")
			}
		}
	}
	return b.String()
}

func header(r Report) string {
	var parts []string
	for _, s := range r.Sides {
		parts = append(parts, sideRu(s.Name)+" |")
	}
	return strings.Join(parts, " ")
}

func sideRu(name string) string {
	switch name {
	case Local:
		return "локально"
	case Cloud:
		return "облако"
	}
	return name
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
