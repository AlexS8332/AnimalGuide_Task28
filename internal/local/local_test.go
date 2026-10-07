package local

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":                          DefaultBaseURL,
		"http://10.0.0.5:11434/":    "http://10.0.0.5:11434",
		"http://127.0.0.1:1234/v1":  "http://127.0.0.1:1234",
		"http://127.0.0.1:1234/v1/": "http://127.0.0.1:1234",
		"192.168.1.243:11434":       "http://192.168.1.243:11434",
	}
	for in, want := range cases {
		c := Config{BaseURL: in}.Normalize()
		if c.BaseURL != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, c.BaseURL, want)
		}
		if c.Model != DefaultModel || c.Reasoning != DefaultReasoning {
			t.Errorf("умолчания не подставлены: %+v", c)
		}
	}
	if got := (Config{BaseURL: "http://x:1/v1"}).OpenAIBase(); got != "http://x:1/v1" {
		t.Errorf("OpenAIBase = %q", got)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("LOCAL_LLM_BASE_URL", "http://h:9/v1")
	t.Setenv("LOCAL_LLM_MODEL", "qwen3.5:4b")
	t.Setenv("LOCAL_LLM_REASONING", "-")
	c := FromEnv()
	if c.BaseURL != "http://h:9" || c.Model != "qwen3.5:4b" || c.Reasoning != "-" {
		t.Fatalf("FromEnv = %+v", c)
	}
	if c.Client().ReasoningEffort != "" {
		t.Error("«-» не должен отправлять reasoning_effort")
	}
	t.Setenv("LOCAL_LLM_REASONING", "")
	if FromEnv().Client().ReasoningEffort != "none" {
		t.Error("по умолчанию reasoning_effort=none")
	}
}

func fakeOllama(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"version":"0.40.0"}`) })
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"models":[{"name":"qwen3.5:9b","size":7000000000,"details":{"format":"gguf","family":"qwen35","parameter_size":"9B","quantization_level":"Q4_K_M"},"capabilities":["completion","tools","thinking"]},
			{"name":"bge-m3:latest","size":1,"details":{},"capabilities":["embedding"]}]}`)
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"models":[{"name":"qwen3.5:9b","size_vram":7500000000,"context_length":16384}]}`)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["reasoning_effort"] != "none" {
			t.Errorf("reasoning_effort = %v", req["reasoning_effort"])
		}
		io.WriteString(w, `{"model":"qwen3.5:9b","choices":[{"message":{"role":"assistant","content":"Ursus arctos"},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":5,"total_tokens":35}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProbeOllama(t *testing.T) {
	srv := fakeOllama(t)
	st := Probe(context.Background(), Config{BaseURL: srv.URL, Model: "qwen3.5:9b"}, nil)
	if !st.OK || st.Kind != "ollama" || st.Version != "0.40.0" {
		t.Fatalf("Probe = %+v", st)
	}
	if !st.ModelFound || len(st.Models) != 2 || len(st.Loaded) != 1 {
		t.Fatalf("модели: %+v", st)
	}
	if m := st.Models[1]; m.Name != "qwen3.5:9b" || !m.Tools || m.Params != "9B" {
		t.Errorf("модель: %+v", m)
	}
	if !st.Models[0].Embed {
		t.Error("bge-m3 — эмбеддинги")
	}
	if st.Loaded[0].Context != 16384 {
		t.Errorf("контекст: %+v", st.Loaded[0])
	}
	// latest-суффикс считается той же моделью.
	if st := Probe(context.Background(), Config{BaseURL: srv.URL, Model: "bge-m3"}, nil); !st.ModelFound {
		t.Error("bge-m3 = bge-m3:latest")
	}
	if st := Probe(context.Background(), Config{BaseURL: srv.URL, Model: "llama3"}, nil); st.ModelFound || !strings.Contains(st.Hint, "ollama pull") {
		t.Errorf("нет модели — подсказка: %+v", st)
	}
}

func TestProbeOpenAIOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"qwen2.5-7b-instruct"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	st := Probe(context.Background(), Config{BaseURL: srv.URL + "/v1", Model: "qwen2.5-7b-instruct"}, nil)
	if !st.OK || st.Kind != "openai" || !st.ModelFound {
		t.Fatalf("Probe = %+v", st)
	}
}

func TestProbeDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	st := Probe(context.Background(), Config{BaseURL: srv.URL}, nil)
	if st.OK || st.Error == "" || st.Hint == "" {
		t.Fatalf("сервер выключен: %+v", st)
	}
}

func TestRunAgainstServer(t *testing.T) {
	srv := fakeOllama(t)
	cfg := Config{BaseURL: srv.URL, Model: "qwen3.5:9b"}
	ch, _ := Find("fact")
	r := Run(context.Background(), cfg.Client(), cfg.Model, ch)
	if r.Error != "" || r.OK == nil || !*r.OK || r.Usage.Completion != 5 {
		t.Fatalf("Run = %+v", r)
	}
	rare, _ := Find("rare")
	if r := Run(context.Background(), cfg.Client(), cfg.Model, rare); r.OK == nil || *r.OK {
		t.Fatalf("Ursus arctos — не манул: %+v", r)
	}
}

type canned struct{ resp llm.Response }

func (c canned) Chat(context.Context, llm.Request) (llm.Response, error) { return c.resp, nil }

func reply(text string) canned {
	return canned{llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: text}}}
}

func verdict(t *testing.T, id string, c llm.Chatter) Result {
	t.Helper()
	ch, ok := Find(id)
	if !ok {
		t.Fatalf("нет проверки %s", id)
	}
	return Run(context.Background(), c, "m", ch)
}

func TestVerifiers(t *testing.T) {
	cases := []struct {
		id   string
		c    llm.Chatter
		want bool
	}{
		{"fact", reply("Ursus arctos"), true},
		{"fact", reply("Это Ursus arctos — бурый медведь, крупный хищник семейства медвежьих, обитает в лесах Евразии."), false},
		{"json", reply(`{"name":"Ирбис","latin":"Panthera uncia","family":"Кошачьи","countries":["Китай","Монголия","Непал"]}`), true},
		{"json", reply("```json\n{\"name\":\"Ирбис\",\"latin\":\"Panthera uncia\",\"family\":\"кошачьи\",\"countries\":[\"Китай\",\"Монголия\",\"Непал\"]}\n```"), true},
		{"json", reply(`{"latin":"Uncia uncia","family":"Кошачьи","countries":["Китай"]}`), false},
		{"json", reply(`Вот JSON: {}`), false},
		{"reason", reply("35 / 5 = 7\nОтвет: 7"), true},
		{"reason", reply("Ответ: 7,0"), true},
		{"reason", reply("25 / 5 = 5\nОтвет: 5"), false},
		{"reason", reply("примерно семь"), false},
		{"tool", reply("Росомаха весит 10–20 кг."), false},
		{"tool", canned{llm.Response{Message: llm.Message{ToolCalls: []llm.ToolCall{{Function: llm.FunctionCall{Name: "kb_search", Arguments: `{"query":"масса росомахи"}`}}}}}}, true},
		{"tool", canned{llm.Response{Message: llm.Message{ToolCalls: []llm.ToolCall{{Function: llm.FunctionCall{Name: "kb_search", Arguments: `{"query":"барсук"}`}}}}}}, false},
	}
	for i, c := range cases {
		r := verdict(t, c.id, c.c)
		if r.OK == nil || *r.OK != c.want {
			t.Errorf("%d %s: ok=%v (%s), want %v", i, c.id, r.OK, r.Verdict, c.want)
		}
	}
}

func TestChecksShape(t *testing.T) {
	levels := map[string]int{}
	for _, c := range Checks() {
		levels[c.Level]++
		if c.Prompt == "" || c.Expect == "" || c.verify == nil {
			t.Errorf("%s: неполная проверка", c.ID)
		}
	}
	if len(Checks()) < 3 || levels[LevelSimple] == 0 || levels[LevelComplex] == 0 {
		t.Fatalf("нужно минимум 3 запроса разной сложности: %v", levels)
	}
}

func TestMarkdownAndSummary(t *testing.T) {
	ok, no := true, false
	rep := Report{Status: Status{Config: Config{BaseURL: "http://h", Model: "m"}.Normalize(), OK: true},
		Results: []Result{{Title: "a|b", OK: &ok, TookMS: 1000, TokPerS: 10}, {OK: &no, TookMS: 3000, TokPerS: 20}, {Error: "x", TookMS: 2000}}}
	s := Summarize(rep.Results)
	if s.Passed != 1 || s.Failed != 1 || s.Errors != 1 || s.MedianMS != 2000 || s.TokPerS != 15 {
		t.Fatalf("Summarize = %+v", s)
	}
	md := Markdown(rep)
	if !strings.Contains(md, "**1 из 3**") || !strings.Contains(md, `a\|b`) {
		t.Fatalf("markdown:\n%s", md)
	}
}

func TestThinkConfig(t *testing.T) {
	t.Setenv("LOCAL_LLM_THINK", "")
	if c := FromEnv().Client(); c.ThinkEffort != DefaultThink || c.DefaultMaxTokens != DefaultMaxTokens {
		t.Fatalf("умолчания: think=%q max=%d", c.ThinkEffort, c.DefaultMaxTokens)
	}
	t.Setenv("LOCAL_LLM_THINK", "none")
	if c := FromEnv().Client(); c.ThinkEffort != "" {
		t.Fatalf("none выключает рассуждение агентов: %q", c.ThinkEffort)
	}
}
