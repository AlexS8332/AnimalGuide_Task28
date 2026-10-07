// Команда local — локальная LLM из консоли (v26): проверить сервер,
// задать вопрос, прогнать четыре запроса разной сложности.
//
//	local status                        # сервер, версия, модели, что загружено
//	local ask "Чем питается манул?"     # свободный вопрос
//	local bench                         # 4 проверки: простой → сложный
//	local bench -runs 3 -out examples/local/bench.md
//
// Адрес и модель — LOCAL_LLM_BASE_URL и LOCAL_LLM_MODEL (в окружении или в
// .env/.env.local), по умолчанию http://127.0.0.1:11434 и qwen3.5:9b.
// Облачный ключ не нужен.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/local"
)

func main() {
	loadEnv()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "status":
		err = status(os.Args[2:])
	case "ask":
		err = ask(os.Args[2:])
	case "bench":
		err = bench(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `local — локальная LLM (Ollama, LM Studio, llama.cpp)

  local status [-json]
  local ask [-model M] [-system S] [-max N] "вопрос"
  local bench [-model M] [-runs N] [-only fact,json,tool,reason] [-out файл.md] [-json файл.json]

Адрес — LOCAL_LLM_BASE_URL (http://127.0.0.1:11434), модель — LOCAL_LLM_MODEL (qwen3.5:9b),
рассуждение — LOCAL_LLM_REASONING (none; «-» — не отправлять).`)
}

func config(model string) local.Config {
	c := local.FromEnv()
	if model != "" {
		c.Model = model
	}
	return c.Normalize()
}

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "вывести JSON")
	fs.Parse(args)
	st := local.Probe(context.Background(), config(""), nil)
	if *asJSON {
		return printJSON(st)
	}
	fmt.Print(local.StatusText(st))
	if !st.OK {
		return fmt.Errorf("сервер не отвечает")
	}
	return nil
}

func ask(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	model := fs.String("model", "", "модель (по умолчанию LOCAL_LLM_MODEL)")
	system := fs.String("system", "", "системный промпт")
	max := fs.Int("max", 1024, "предел токенов ответа")
	fs.Parse(args)
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("нужен вопрос: local ask \"Чем питается манул?\"")
	}
	cfg := config(*model)
	r := local.Ask(context.Background(), cfg.Client(), cfg.Model, *system, prompt, *max)
	if r.Error != "" {
		return fmt.Errorf("%s", r.Error)
	}
	fmt.Println(r.Answer)
	fmt.Fprintf(os.Stderr, "\n— %s · %s · %d → %d ток. · %.1f ток/с\n", r.Model, ms(r.TookMS), r.Usage.Prompt, r.Usage.Completion, r.TokPerS)
	return nil
}

func bench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	model := fs.String("model", "", "модель (по умолчанию LOCAL_LLM_MODEL)")
	runs := fs.Int("runs", 1, "сколько раз повторить каждый запрос")
	only := fs.String("only", "", "только эти проверки через запятую")
	out := fs.String("out", "", "записать отчёт markdown")
	jsonOut := fs.String("json", "", "записать результаты JSON")
	fs.Parse(args)

	cfg := config(*model)
	ctx := context.Background()
	st := local.Probe(ctx, cfg, nil)
	fmt.Print(local.StatusText(st))
	if !st.OK {
		return fmt.Errorf("сервер не отвечает")
	}
	want := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	client := cfg.Client()
	var results []local.Result
	for _, ch := range local.Checks() {
		if len(want) > 0 && !want[ch.ID] {
			continue
		}
		for i := 0; i < *runs; i++ {
			fmt.Printf("\n[%s · %s] %s\n", ch.Level, ch.ID, ch.Title)
			r := local.Run(ctx, client, cfg.Model, ch)
			results = append(results, r)
			fmt.Print(local.ResultText(r))
		}
	}
	rep := local.Report{At: time.Now(), Status: st, Results: results}
	fmt.Println()
	fmt.Print(local.SummaryText(rep))
	if *out != "" {
		if err := write(*out, []byte(local.Markdown(rep))); err != nil {
			return err
		}
		fmt.Println("отчёт:", *out)
	}
	if *jsonOut != "" {
		raw, _ := json.MarshalIndent(rep, "", "  ")
		if err := write(*jsonOut, raw); err != nil {
			return err
		}
		fmt.Println("JSON:", *jsonOut)
	}
	return nil
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func ms(v int64) string { return fmt.Sprintf("%.1f с", float64(v)/1000) }

// loadEnv — .env.local и .env в рабочем каталоге и выше; окружение важнее.
func loadEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		for _, name := range []string{".env.local", ".env"} {
			loadFile(filepath.Join(dir, name))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

// bom — метка порядка байтов в начале файла (её оставляет Блокнот).
const bom = string(rune(0xFEFF))

func loadFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), bom))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if !ok || k == "" || v == "" {
			continue
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}
