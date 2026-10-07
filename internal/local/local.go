// Package local — локальная LLM (v26): сервер с OpenAI-совместимым API на
// своей машине или в своей сети — Ollama, LM Studio, llama.cpp server.
//
// Пакет отвечает на три вопроса задания: запускается ли модель (Probe —
// сервер, версия, модели, что загружено в память), можно ли к ней
// обратиться по HTTP API (Client — тот же llm.Chatter, что у облачной
// модели) и отвечает ли она (Checks — четыре запроса разной сложности с
// проверкой ответа кодом).
//
// Адрес — корень сервера (http://127.0.0.1:11434); OpenAI-совместимый API
// лежит под /v1. Адрес с /v1 на конце тоже принимается: так его пишут для
// LM Studio и llama.cpp.
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
)

const (
	// DefaultBaseURL — Ollama на этой машине.
	DefaultBaseURL = "http://127.0.0.1:11434"
	// DefaultModel — Qwen3.5 9B: на 8–16 ГБ памяти помещается целиком,
	// русский, вызов инструментов.
	DefaultModel = "qwen3.5:9b"
	// DefaultReasoning — рассуждение выключено: агенты справочника ждут
	// короткий ответ, а Qwen3.5 по умолчанию «думает» сотни токенов.
	DefaultReasoning = "none"
	// DefaultMaxTokens — предел ответа, если агент его не задал.
	DefaultMaxTokens = 4096
	// DefaultThink — рассуждение агентов с протоколом: без него 9B не
	// вызывает plan и approve и выдумывает «план утверждён».
	DefaultThink = "medium"

	probeTimeout = 5 * time.Second
	// requestTimeout — локальная модель медленнее облачной: первый запрос
	// ещё и загружает её в память (десятки секунд).
	requestTimeout = 5 * time.Minute
)

// Config — где живёт локальная модель.
type Config struct {
	BaseURL   string `json:"baseUrl"`   // корень сервера, без /v1
	Model     string `json:"model"`     // имя модели на сервере
	Reasoning string `json:"reasoning"` // reasoning_effort; "-" — не отправлять
	// Think — reasoning_effort для агентов с многошаговым протоколом
	// (подборка, длинный флоу); «none» — рассуждения нет ни у кого.
	Think string `json:"think"`
}

// FromEnv — LOCAL_LLM_BASE_URL, LOCAL_LLM_MODEL, LOCAL_LLM_REASONING или
// умолчания.
func FromEnv() Config {
	c := Config{
		BaseURL:   strings.TrimSpace(os.Getenv("LOCAL_LLM_BASE_URL")),
		Model:     strings.TrimSpace(os.Getenv("LOCAL_LLM_MODEL")),
		Reasoning: strings.TrimSpace(os.Getenv("LOCAL_LLM_REASONING")),
		Think:     strings.TrimSpace(os.Getenv("LOCAL_LLM_THINK")),
	}
	return c.Normalize()
}

// Normalize подставляет умолчания и срезает /v1 и хвостовой слэш.
func (c Config) Normalize() Config {
	b := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	b = strings.TrimSuffix(b, "/v1")
	if b == "" {
		b = DefaultBaseURL
	}
	if !strings.Contains(b, "://") {
		b = "http://" + b
	}
	c.BaseURL = b
	if strings.TrimSpace(c.Model) == "" {
		c.Model = DefaultModel
	}
	if c.Reasoning == "" {
		c.Reasoning = DefaultReasoning
	}
	if c.Think == "" {
		c.Think = DefaultThink
	}
	return c
}

// OpenAIBase — адрес OpenAI-совместимого API.
func (c Config) OpenAIBase() string { return c.Normalize().BaseURL + "/v1" }

// Client — клиент чата локальной модели. Ключ серверу не нужен, но
// заголовок Authorization клиент шлёт всегда — пусть будет заглушка.
func (c Config) Client() *llm.Client {
	c = c.Normalize()
	cl := llm.NewClient("local", c.OpenAIBase())
	cl.HTTP = &http.Client{Timeout: requestTimeout}
	if c.Reasoning != "-" {
		cl.ReasoningEffort = c.Reasoning
	}
	cl.DefaultMaxTokens = DefaultMaxTokens
	if c.Think != "none" && c.Think != "-" {
		cl.ThinkEffort = c.Think
	}
	return cl
}

// Model — модель на сервере.
type Model struct {
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`     // байт на диске
	Params   string `json:"params,omitempty"`   // 9B
	Quant    string `json:"quant,omitempty"`    // Q4_K_M, nvfp4
	Family   string `json:"family,omitempty"`   // qwen35
	Format   string `json:"format,omitempty"`   // gguf, safetensors
	Embed    bool   `json:"embed,omitempty"`    // только эмбеддинги
	Tools    bool   `json:"tools,omitempty"`    // умеет вызывать инструменты
	Thinking bool   `json:"thinking,omitempty"` // умеет рассуждать
}

// Loaded — модель, загруженная в память прямо сейчас (Ollama /api/ps).
type Loaded struct {
	Name    string `json:"name"`
	VRAM    int64  `json:"vram"`
	Context int    `json:"context,omitempty"`
	Until   string `json:"until,omitempty"` // когда выгрузится
}

// Status — ответ Probe.
type Status struct {
	OK         bool     `json:"ok"`
	Config     Config   `json:"config"`
	Kind       string   `json:"kind,omitempty"` // ollama | openai
	Version    string   `json:"version,omitempty"`
	Models     []Model  `json:"models"`
	Loaded     []Loaded `json:"loaded"`
	ModelFound bool     `json:"modelFound"`
	TookMS     int64    `json:"tookMs"`
	Error      string   `json:"error,omitempty"`
	Hint       string   `json:"hint,omitempty"`
}

// Probe опрашивает сервер: сначала родной API Ollama (версия, модели,
// загруженные), а если его нет — OpenAI-совместимый /v1/models (LM Studio,
// llama.cpp). Модель не вызывается: проверка дешёвая и быстрая.
func Probe(ctx context.Context, cfg Config, hc *http.Client) Status {
	cfg = cfg.Normalize()
	if hc == nil {
		hc = &http.Client{Timeout: probeTimeout}
	}
	started := time.Now()
	st := Status{Config: cfg, Models: []Model{}, Loaded: []Loaded{}}
	defer func() { st.TookMS = time.Since(started).Milliseconds() }()

	var ver struct {
		Version string `json:"version"`
	}
	if err := getJSON(ctx, hc, cfg.BaseURL+"/api/version", &ver); err == nil && ver.Version != "" {
		st.Kind, st.Version = "ollama", ver.Version
		if err := ollamaModels(ctx, hc, cfg.BaseURL, &st); err != nil {
			st.Error = err.Error()
			return finish(st)
		}
		ollamaLoaded(ctx, hc, cfg.BaseURL, &st)
		st.OK = true
		return finish(st)
	}

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, hc, cfg.OpenAIBase()+"/models", &list); err != nil {
		st.Error = err.Error()
		st.Hint = "запустите сервер (ollama serve, LM Studio, llama-server) и проверьте LOCAL_LLM_BASE_URL"
		return finish(st)
	}
	st.Kind, st.OK = "openai", true
	for _, m := range list.Data {
		st.Models = append(st.Models, Model{Name: m.ID})
	}
	return finish(st)
}

func finish(st Status) Status {
	sort.Slice(st.Models, func(i, j int) bool { return st.Models[i].Name < st.Models[j].Name })
	for _, m := range st.Models {
		if m.Name == st.Config.Model || strings.TrimSuffix(m.Name, ":latest") == st.Config.Model {
			st.ModelFound = true
		}
	}
	if st.OK && !st.ModelFound && st.Hint == "" {
		st.Hint = fmt.Sprintf("модели %s на сервере нет: ollama pull %s или задайте LOCAL_LLM_MODEL", st.Config.Model, st.Config.Model)
	}
	return st
}

func ollamaModels(ctx context.Context, hc *http.Client, base string, st *Status) error {
	var tags struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				Format string `json:"format"`
				Family string `json:"family"`
				Params string `json:"parameter_size"`
				Quant  string `json:"quantization_level"`
			} `json:"details"`
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := getJSON(ctx, hc, base+"/api/tags", &tags); err != nil {
		return err
	}
	for _, m := range tags.Models {
		x := Model{Name: m.Name, Size: m.Size, Params: m.Details.Params, Quant: m.Details.Quant,
			Family: m.Details.Family, Format: m.Details.Format}
		for _, c := range m.Capabilities {
			switch c {
			case "tools":
				x.Tools = true
			case "thinking":
				x.Thinking = true
			case "embedding":
				x.Embed = true
			}
		}
		st.Models = append(st.Models, x)
	}
	return nil
}

func ollamaLoaded(ctx context.Context, hc *http.Client, base string, st *Status) {
	var ps struct {
		Models []struct {
			Name      string `json:"name"`
			SizeVRAM  int64  `json:"size_vram"`
			ExpiresAt string `json:"expires_at"`
			Context   int    `json:"context_length"`
		} `json:"models"`
	}
	if getJSON(ctx, hc, base+"/api/ps", &ps) != nil {
		return
	}
	for _, m := range ps.Models {
		st.Loaded = append(st.Loaded, Loaded{Name: m.Name, VRAM: m.SizeVRAM, Context: m.Context, Until: m.ExpiresAt})
	}
}

func getJSON(ctx context.Context, hc *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("сервер %s не отвечает: %w", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: разбор ответа: %w", url, err)
	}
	return nil
}
