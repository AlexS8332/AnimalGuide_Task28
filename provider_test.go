package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
)

func TestProviderName(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")
	for in, want := range map[string]string{"": ProviderCloud, "cloud": ProviderCloud, "deepseek": ProviderCloud, "LOCAL": ProviderLocal, "ollama": ProviderLocal} {
		if got, err := providerName(in); err != nil || got != want {
			t.Errorf("providerName(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := providerName("openai"); err == nil {
		t.Error("неизвестный провайдер принят")
	}
	t.Setenv("LLM_PROVIDER", "local")
	if got, _ := providerName(""); got != ProviderLocal {
		t.Errorf("LLM_PROVIDER не учтён: %q", got)
	}
	if got, _ := providerName("cloud"); got != ProviderCloud {
		t.Errorf("флаг важнее окружения: %q", got)
	}
}

func TestCloudProviderNeedsKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	if _, err := newProvider(ProviderCloud); err == nil || !strings.Contains(err.Error(), "-llm local") {
		t.Fatalf("без ключа: %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "k")
	t.Setenv("DEEPSEEK_MODEL", "")
	p, err := newProvider(ProviderCloud)
	if err != nil || p.Model != llm.DefaultModel || p.contextLimit(1000, false) != 1000 || !p.meta()["cloud"].(bool) {
		t.Fatalf("облако: %+v %v", p, err)
	}
}

func fakeServer(t *testing.T, ctx int, model string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"version":"0.40.0"}`) })
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"models":[{"name":"`+model+`","capabilities":["tools"]}]}`)
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"models":[{"name":"`+model+`","context_length":`+itoa(ctx)+`}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestLocalProvider(t *testing.T) {
	srv := fakeServer(t, 16384, "qwen3.5:9b")
	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL)
	t.Setenv("LOCAL_LLM_MODEL", "qwen3.5:9b")
	t.Setenv("LOCAL_LLM_CONTEXT", "")
	t.Setenv("DEEPSEEK_API_KEY", "секрет")
	p, err := newProvider(ProviderLocal)
	if err != nil {
		t.Fatal(err)
	}
	if _, set := os.LookupEnv("DEEPSEEK_API_KEY"); set {
		t.Fatal("облачный ключ остался в окружении локального режима")
	}
	if p.Context != 16384 || p.contextLimit(defaultContextLimit, false) != 16384-localReserve || p.contextLimit(5000, true) != 5000 {
		t.Fatalf("окно: %d, лимит %d", p.Context, p.contextLimit(defaultContextLimit, false))
	}
	if c := llm.PriceOf("qwen3.5:9b", llm.Usage{Prompt: 1000, Completion: 100}, time.Now()); !c.Known || c.USD != 0 || c.Tariff != llm.TariffLocal {
		t.Fatalf("цена локальной модели: %+v", c)
	}
	if m := p.meta(); m["name"] != ProviderLocal || m["cloud"].(bool) || !strings.Contains(m["label"].(string), "локальная") {
		t.Fatalf("meta: %+v", m)
	}
	t.Setenv("LOCAL_LLM_CONTEXT", "8192")
	if p, _ := newProvider(ProviderLocal); p.Context != 8192 {
		t.Fatalf("LOCAL_LLM_CONTEXT: %d", p.Context)
	}
}

func TestLocalProviderFailsLoudly(t *testing.T) {
	srv := fakeServer(t, 16384, "llama3:8b")
	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL)
	t.Setenv("LOCAL_LLM_MODEL", "qwen3.5:9b")
	if _, err := newProvider(ProviderLocal); err == nil || !strings.Contains(err.Error(), "ollama pull") {
		t.Fatalf("модели нет: %v", err)
	}
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", down.URL)
	if _, err := newProvider(ProviderLocal); err == nil || !strings.Contains(err.Error(), "не отвечает") {
		t.Fatalf("сервер выключен: %v", err)
	}
}
