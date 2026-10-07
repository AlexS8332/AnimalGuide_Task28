package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/compare"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/kb"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/local"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/rag"
)

func init() {
	register("compare", "RAG локально против облака: один конвейер, два генератора — качество, скорость, стабильность → examples/compare/report.md (и JSON)", runCompare)
}

// defaultCompareReport — отчёт сравнения сторон (рядом — report.json).
const defaultCompareReport = "examples/compare/report.md"

func runCompare(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlagSet("compare", "[флаги]", errOut)
	questions := fs.String("questions", "eval/questions.json", "контрольные вопросы")
	splits := fs.String("splits", "test,out", "наборы через запятую: test, dev, out")
	mode := fs.String("mode", string(rag.RAGCite), "режим ответа: rag+cite (источники и цитаты), rag, rag+both…")
	runs := fs.Int("runs", 3, "повторов каждого вопроса у каждой стороны (стабильность)")
	sides := fs.String("sides", "local,cloud", "стороны через запятую: local, cloud")
	judge := fs.String("judge", "auto", "судья для обеих сторон: cloud, local, none; auto — cloud, если есть DEEPSEEK_API_KEY, иначе local")
	mdPath := fs.String("out", defaultCompareReport, "куда записать отчёт markdown (пусто — не писать); JSON — рядом, с расширением .json")
	af := newAnswerFlags(fs)
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	m := rag.Mode(strings.TrimSpace(*mode))
	if !m.Known() || !m.UsesBase() {
		fmt.Fprintf(errOut, "ошибка: режим %q — нужен режим с базой: rag, rag+filter, rag+rewrite, rag+both, rag+cite\n", m)
		return exitUsage
	}
	if *runs < 1 {
		fmt.Fprintln(errOut, "ошибка: -runs ≥ 1")
		return exitUsage
	}
	qs, err := kb.LoadQuestions(*questions)
	if err != nil {
		fmt.Fprintln(errOut, "ошибка:", err)
		return exitUsage
	}
	// Поиск — один на обе стороны: база, индекс, эмбеддер, конвейер.
	base, closeKB, code := af.answerer(ctx, true, errOut)
	if code >= 0 {
		return code
	}
	defer closeKB()

	var list []compare.Side
	for _, name := range splitList(*sides) {
		c, model, err := modelFor(name)
		if err != nil {
			fmt.Fprintf(errOut, "ошибка: сторона %s: %v\n", name, err)
			return exitUsage
		}
		a := *base
		a.LLM, a.Model = c, model
		list = append(list, compare.Side{Name: sideName(name), Label: sideLabel(name, model), Answerer: &a})
	}
	if len(list) == 0 {
		fmt.Fprintln(errOut, "ошибка: не задано ни одной стороны")
		return exitUsage
	}

	o := compare.Options{Splits: splitList(*splits), Mode: m, Repeats: *runs,
		Progress: func(side string, row rag.Row) {
			fmt.Fprintf(errOut, "  %-5s %s %s: %s, смен %d\n", side, row.Question.ID, row.Question.Split, orDash(string(row.Majority[m])), row.Flips[m])
		}}
	switch j := pickJudge(*judge); j {
	case "none":
	case compare.Local, compare.Cloud:
		c, model, err := modelFor(j)
		if err != nil {
			fmt.Fprintln(errOut, "ошибка: судья:", err)
			return exitUsage
		}
		o.Judge = &rag.Judge{LLM: c, Model: model}
		o.JudgeLabel = sideLabel(j, model)
	default:
		fmt.Fprintf(errOut, "ошибка: -judge %q — cloud, local, none или auto\n", *judge)
		return exitUsage
	}
	fmt.Fprintf(errOut, "kb compare: режим %s, наборы %s, повторов %d, судья — %s\n", m, *splits, *runs, orDash(o.JudgeLabel))
	r, err := compare.Run(ctx, list, qs, o)
	if err != nil {
		fmt.Fprintln(errOut, "ошибка:", err)
		return exitFailed
	}
	for _, line := range r.Conclusion {
		fmt.Fprintln(out, "- "+line)
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	for _, s := range r.Sides {
		if why := ragFallback(s.Report); why != "" && !explicit["out"] {
			fmt.Fprintf(errOut, "ошибка: векторный поиск не состоялся (%s) — отчёт не записан в %s.\n"+
				"  Поднимите эмбеддер (-embed-url, EMBED_BASE_URL) или укажите путь отчёта явно: -out <файл>.\n", why, *mdPath)
			return exitFailed
		}
	}
	if *mdPath == "" {
		return exitOK
	}
	if err := writeFile(*mdPath, []byte(r.Markdown())); err != nil {
		fmt.Fprintln(errOut, "ошибка:", err)
		return exitFailed
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	jsonPath := strings.TrimSuffix(*mdPath, filepath.Ext(*mdPath)) + ".json"
	if err == nil {
		err = writeFile(jsonPath, append(raw, '\n'))
	}
	if err != nil {
		fmt.Fprintln(errOut, "ошибка:", err)
		return exitFailed
	}
	fmt.Fprintln(out, "Отчёт:", *mdPath)
	fmt.Fprintln(out, "JSON:", jsonPath)
	return exitOK
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sideName(provider string) string {
	switch strings.ToLower(provider) {
	case "local", "ollama":
		return compare.Local
	}
	return compare.Cloud
}

// sideLabel — «локальная · qwen3.5:9b @ http://…» или «облачная · deepseek-…».
func sideLabel(provider, model string) string {
	if sideName(provider) == compare.Local {
		return "локальная · " + model + " @ " + local.FromEnv().BaseURL
	}
	return "облачная · " + model
}

// pickJudge — auto: облачный судья, если есть ключ (сильнее и не судит
// сам себя у локальной стороны), иначе локальный.
func pickJudge(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v != "auto" {
		return v
	}
	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) != "" {
		return compare.Cloud
	}
	return compare.Local
}
