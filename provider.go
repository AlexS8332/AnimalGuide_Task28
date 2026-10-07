package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/agent"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
	localllm "github.com/AlexS8332/AnimalGuide_Task28/internal/local"
)

// Провайдеры модели (v27).
const (
	ProviderCloud = "cloud" // DeepSeek по ключу
	ProviderLocal = "local" // свой сервер: Ollama, LM Studio, llama.cpp
)

// localReserve — сколько токенов окна локальной модели оставить на ответ.
// У Ollama num_ctx — это запрос плюс ответ, и переполнение он не
// отклоняет, а молча срезает начало запроса — вместе с системным промптом.
const localReserve = 2048

// defaultLocalContext — окно, если сервер его не сообщил.
const defaultLocalContext = 16384

// provider — чем отвечает приложение: клиент, модель и то, что о них
// показать человеку.
type provider struct {
	Name    string // cloud | local
	LLM     llm.Chatter
	Model   string
	Label   string // для шапки: «локальная · qwen3.5:9b @ 192.168.1.243»
	BaseURL string
	// Context — окно модели в токенах (0 — неизвестно: облако).
	Context int
	Local   localllm.Config
	Probe   *localllm.Status
}

// providerName — флаг, иначе LLM_PROVIDER, иначе cloud.
func providerName(flagValue string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(flagValue))
	if v == "" {
		v = strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	}
	switch v {
	case "", ProviderCloud, "deepseek":
		return ProviderCloud, nil
	case ProviderLocal, "ollama":
		return ProviderLocal, nil
	}
	return "", fmt.Errorf("неизвестный провайдер модели %q; допустимы cloud и local", v)
}

// newProvider собирает провайдера. Локальный режим не читает облачный
// ключ и убирает его из окружения процесса: ни один агент, ни дочерний
// MCP-сервер не сможет обратиться к облачной модели даже по ошибке.
func newProvider(name string) (provider, error) {
	if name == ProviderLocal {
		return newLocalProvider(context.Background())
	}
	key := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if key == "" {
		return provider{}, fmt.Errorf("не задан DEEPSEEK_API_KEY — задай переменную окружения или впиши ключ в .env.local (см. .env.example); без облака — -llm local")
	}
	model := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = llm.DefaultModel
	}
	c := llm.NewClient(key, os.Getenv("DEEPSEEK_BASE_URL"))
	return provider{Name: ProviderCloud, LLM: c, Model: model, BaseURL: c.BaseURL, Label: "облачная · " + model}, nil
}

func newLocalProvider(ctx context.Context) (provider, error) {
	for _, k := range []string{"DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL"} {
		os.Unsetenv(k)
	}
	cfg := localllm.FromEnv()
	st := localllm.Probe(ctx, cfg, nil)
	p := provider{Name: ProviderLocal, LLM: cfg.Client(), Model: cfg.Model, BaseURL: cfg.BaseURL,
		Local: cfg, Probe: &st, Label: "локальная · " + cfg.Model}
	llm.RegisterLocal(cfg.Model)
	p.Context = localContext(st)
	if !st.OK {
		return p, fmt.Errorf("локальная модель: сервер %s не отвечает (%s) — %s", cfg.BaseURL, st.Error, st.Hint)
	}
	if !st.ModelFound {
		return p, fmt.Errorf("локальная модель: %s", st.Hint)
	}
	return p, nil
}

// localContext — окно модели: LOCAL_LLM_CONTEXT, иначе контекст
// загруженной модели из /api/ps, иначе 16384.
func localContext(st localllm.Status) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("LOCAL_LLM_CONTEXT"))); err == nil && v > 0 {
		return v
	}
	for _, l := range st.Loaded {
		if (l.Name == st.Config.Model || strings.TrimSuffix(l.Name, ":latest") == st.Config.Model) && l.Context > 0 {
			return l.Context
		}
	}
	return defaultLocalContext
}

// contextLimit — свой лимит запроса: у облака — флаг, у локальной модели —
// окно минус запас на ответ (если флаг не задан явно).
func (p provider) contextLimit(flagValue int, flagSet bool) int {
	if p.Name != ProviderLocal || flagSet {
		return flagValue
	}
	if p.Context > localReserve*2 {
		return p.Context - localReserve
	}
	return p.Context
}

// meta — сведения для шапки интерфейса.
func (p provider) meta() map[string]any {
	m := map[string]any{"name": p.Name, "label": p.Label, "model": p.Model, "baseUrl": p.BaseURL, "context": p.Context}
	m["cloud"] = p.Name == ProviderCloud
	return m
}

// providerTail — хвост строки «модель» стартового вывода.
func providerTail(p provider, r agent.Runner) string {
	if p.Name != ProviderLocal {
		return ""
	}
	return fmt.Sprintf(" @ %s (окно %d, лимит запроса %d, переполнение — %s); облачные модели в ходах выключены", p.BaseURL, p.Context, r.ContextLimit, r.OnOverflow)
}
