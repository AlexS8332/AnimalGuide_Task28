package local

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report — прогон проверок.
type Report struct {
	At      time.Time `json:"at"`
	Status  Status    `json:"status"`
	Results []Result  `json:"results"`
}

// Summary — итог прогона.
type Summary struct {
	Total, Passed, Failed, Errors int
	TookMS                        int64
	MedianMS                      int64
	TokPerS                       float64 // среднее по ответам
	Prompt, Completion            int
}

// Summarize считает итог.
func Summarize(rs []Result) Summary {
	var s Summary
	var times []int64
	var speed float64
	var nSpeed int
	for _, r := range rs {
		s.Total++
		s.TookMS += r.TookMS
		times = append(times, r.TookMS)
		s.Prompt += r.Usage.Prompt
		s.Completion += r.Usage.Completion
		switch {
		case r.Error != "":
			s.Errors++
		case r.OK != nil && *r.OK:
			s.Passed++
		case r.OK != nil:
			s.Failed++
		}
		if r.TokPerS > 0 {
			speed += r.TokPerS
			nSpeed++
		}
	}
	if len(times) > 0 {
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		s.MedianMS = times[len(times)/2]
	}
	if nSpeed > 0 {
		s.TokPerS = speed / float64(nSpeed)
	}
	return s
}

func gb(b int64) string {
	if b <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f ГБ", float64(b)/(1<<30))
}

// StatusText — состояние сервера для консоли.
func StatusText(st Status) string {
	var b strings.Builder
	fmt.Fprintf(&b, "сервер:  %s", st.Config.BaseURL)
	if st.OK {
		kind := map[string]string{"ollama": "Ollama", "openai": "OpenAI-совместимый"}[st.Kind]
		fmt.Fprintf(&b, " — отвечает (%s %s, %d мс)\n", kind, st.Version, st.TookMS)
	} else {
		fmt.Fprintf(&b, " — НЕ отвечает: %s\n", st.Error)
	}
	mark := "нет на сервере"
	if st.ModelFound {
		mark = "есть"
	}
	fmt.Fprintf(&b, "модель:  %s (%s), reasoning_effort=%s\n", st.Config.Model, mark, st.Config.Reasoning)
	for _, m := range st.Models {
		var tags []string
		for _, t := range []string{m.Params, m.Quant, m.Format, gb(m.Size)} {
			if t != "" {
				tags = append(tags, t)
			}
		}
		if m.Embed {
			tags = append(tags, "эмбеддинги")
		}
		if m.Tools {
			tags = append(tags, "инструменты")
		}
		fmt.Fprintf(&b, "  - %s  %s\n", m.Name, strings.Join(tags, ", "))
	}
	for _, l := range st.Loaded {
		fmt.Fprintf(&b, "в памяти: %s (%s, контекст %d)\n", l.Name, gb(l.VRAM), l.Context)
	}
	if st.Hint != "" {
		fmt.Fprintln(&b, "подсказка:", st.Hint)
	}
	return b.String()
}

func verdictWord(r Result) string {
	switch {
	case r.Error != "":
		return "ОШИБКА"
	case r.OK == nil:
		return "—"
	case *r.OK:
		return "ОК"
	}
	return "НЕ ПРОШЁЛ"
}

// ResultText — один результат для консоли.
func ResultText(r Result) string {
	var b strings.Builder
	if r.Error != "" {
		fmt.Fprintf(&b, "  ошибка: %s\n", r.Error)
		return b.String()
	}
	ans := strings.TrimSpace(r.Answer)
	if ans == "" && len(r.Calls) > 0 {
		ans = "→ " + strings.Join(r.Calls, "; ")
	}
	for _, line := range strings.Split(ans, "\n") {
		fmt.Fprintf(&b, "  │ %s\n", line)
	}
	fmt.Fprintf(&b, "  %s — %s · %.1f с · %d → %d ток. · %.1f ток/с\n", verdictWord(r), r.Verdict,
		float64(r.TookMS)/1000, r.Usage.Prompt, r.Usage.Completion, r.TokPerS)
	return b.String()
}

// SummaryText — итог для консоли.
func SummaryText(rep Report) string {
	s := Summarize(rep.Results)
	return fmt.Sprintf("итог: %d из %d прошли, ошибок %d · всего %.1f с, медиана %.1f с · %.1f ток/с\n",
		s.Passed, s.Total, s.Errors, float64(s.TookMS)/1000, float64(s.MedianMS)/1000, s.TokPerS)
}

func cell(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "|", "\\|")
	return strings.ReplaceAll(s, "\n", "<br>")
}

// Markdown — отчёт прогона.
func Markdown(rep Report) string {
	var b strings.Builder
	st := rep.Status
	fmt.Fprintf(&b, "# Локальная LLM: запросы разной сложности\n\n")
	fmt.Fprintf(&b, "Прогон %s. Команда: `go run ./cmd/local bench`.\n\n", rep.At.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| сервер | %s (%s %s) |\n", st.Config.BaseURL, st.Kind, st.Version)
	fmt.Fprintf(&b, "| модель | `%s`, reasoning_effort=%s |\n", st.Config.Model, st.Config.Reasoning)
	for _, m := range st.Models {
		if m.Name == st.Config.Model {
			fmt.Fprintf(&b, "| вес | %s |\n", strings.Join(nonEmpty(m.Params, m.Quant, m.Format, gb(m.Size)), ", "))
		}
	}
	s := Summarize(rep.Results)
	fmt.Fprintf(&b, "| итог | **%d из %d** прошли проверку кодом, ошибок %d |\n", s.Passed, s.Total, s.Errors)
	fmt.Fprintf(&b, "| время | всего %.1f с, медиана %.1f с, в среднем %.1f ток/с |\n\n", float64(s.TookMS)/1000, float64(s.MedianMS)/1000, s.TokPerS)

	fmt.Fprintf(&b, "| # | уровень | запрос | итог | время | токены | ток/с |\n|---|---|---|---|---|---|---|\n")
	for i, r := range rep.Results {
		fmt.Fprintf(&b, "| %d | %s | %s | %s — %s | %.1f с | %d → %d | %.1f |\n", i+1, r.Level, cell(r.Title),
			verdictWord(r), cell(r.Verdict+r.Error), float64(r.TookMS)/1000, r.Usage.Prompt, r.Usage.Completion, r.TokPerS)
	}
	b.WriteString("\n## Запросы и ответы\n")
	for i, r := range rep.Results {
		fmt.Fprintf(&b, "\n### %d. %s — %s\n\n", i+1, r.Title, r.Level)
		fmt.Fprintf(&b, "**Запрос.**\n\n```text\n%s\n```\n\n", strings.TrimSpace(r.Prompt))
		fmt.Fprintf(&b, "**Ожидается:** %s\n\n", r.Expected)
		ans := strings.TrimSpace(r.Answer)
		if len(r.Calls) > 0 {
			ans = strings.TrimSpace(ans + "\n→ " + strings.Join(r.Calls, "\n→ "))
		}
		if r.Error != "" {
			ans = "ошибка: " + r.Error
		}
		fmt.Fprintf(&b, "**Ответ** (%.1f с):\n\n```text\n%s\n```\n\n", float64(r.TookMS)/1000, ans)
		fmt.Fprintf(&b, "**Проверка:** %s — %s\n", verdictWord(r), r.Verdict)
	}
	return b.String()
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
