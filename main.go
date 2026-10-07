// AnimalGuide — справочник по животным, с которым разговаривают: локальный
// сервер с веб-интерфейсом. Пользователь спрашивает про животное обычными
// словами и получает карточку, собранную только из источников (русская
// Википедия и GBIF), раскрывает её разделы, ходит по дереву классификации,
// сравнивает животных и собирает подборки.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/agent"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/charter"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/compareapi"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/features"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/feed"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/history"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/invariants"
	localllm "github.com/AlexS8332/AnimalGuide_Task28/internal/local"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/localapi"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/netaudit"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/persona"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/server"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/taskapi"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/tokens"
)

// Фронтенд лежит в бинарнике: после `go build` приложение запускается одним
// файлом, без каталога с ассетами рядом.
//
//go:embed web
var webFiles embed.FS

const (
	// Слушаем только петлевой интерфейс: приложение локальное.
	defaultAddr = "127.0.0.1:8770"
	// Общий срок одного хода: подборка — это десятки запросов.
	turnTimeout = 10 * time.Minute
	// defaultContextLimit — свой лимит контекста: у DeepSeek в отказе API
	// стоит 1048576 (1 Mi), изменить его нельзя — max_tokens ограничивает
	// только ответ. Поэтому лимит живёт здесь и ловит переполнение до
	// отправки.
	defaultContextLimit = 1_048_576
)

// options — флаги запуска.
type options struct {
	addr, data, featureSpec, overflow string
	// preset — именованный набор механизмов новых диалогов (features.
	// PresetRAG — «Справочная (RAG)»); -features ложится поверх него.
	preset              string
	mcpServer           string
	open                bool
	window, keep, limit int
	// report — опыт -report вместо сервера: испытания и отчёт в markdown.
	report            bool
	trials, reportOut string
	// factsServer — адрес демона «Интересных фактов»; factsSet — задан ли
	// он флагом явно (тогда и пустая строка — «выключено»).
	factsServer string
	factsSet    bool
	// windowSet — задан ли -window явно: тогда он же и окно справочной по
	// базе (rag.cite), иначе у неё своё умолчание (history.CiteWindow).
	windowSet bool
	// mcpConfig — конфигурация реестра MCP-серверов (окно «MCP-серверы»,
	// длинный флоу); пусто — MCP_CONFIG, иначе встроенная.
	mcpConfig string
	// kb — путь к базе знаний (kb.db); пусто — KB_DB, иначе <data>/kb.db.
	kb string
	// embedURL — адрес эмбеддера; пусто — EMBED_BASE_URL, иначе сайдкар по умолчанию.
	embedURL string
	// provider — модель приложения (v27): cloud | local; пусто — LLM_PROVIDER.
	provider string
	// compare — облачная сторона вкладки «Локально и облако» (v28): cloud; пусто —
	// только генератор приложения.
	compare string
	// limitSet, overflowSet — заданы ли -context-limit и -on-overflow явно:
	// у локальной модели свои умолчания (окно сервера, trim).
	limitSet, overflowSet bool
}

// facts — адрес демона фактов: флаг, иначе TRIVIA_SERVER, иначе адрес по
// умолчанию. Окружение читается здесь, а не в умолчании флага: .env-файлы
// подхватываются уже после разбора флагов.
func (o options) facts() string {
	if o.factsSet {
		return o.factsServer
	}
	if v := strings.TrimSpace(os.Getenv("TRIVIA_SERVER")); v != "" {
		return v
	}
	return feed.DefaultServer
}

// hubConfig — путь к конфигурации реестра серверов: флаг, иначе MCP_CONFIG,
// иначе пусто (встроенная).
func (o options) hubConfig() string {
	if o.mcpConfig != "" {
		return o.mcpConfig
	}
	return strings.TrimSpace(os.Getenv("MCP_CONFIG"))
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.addr, "addr", defaultAddr, "адрес, на котором слушать")
	flag.BoolVar(&o.open, "open", true, "открыть браузер при старте")
	flag.StringVar(&o.data, "data", ".", "каталог данных: history, memory, profiles, collections, invariants")
	flag.StringVar(&o.featureSpec, "features", "", "механизмы новых диалогов поверх умолчаний: «+mcp,-guard», «none,charter», «all»")
	flag.StringVar(&o.preset, "preset", "", "пресет механизмов новых диалогов поверх умолчаний: rag — справочная по базе (rag, rag.filter, rag.rewrite, rag.cite, task); -features — поверх пресета")
	flag.IntVar(&o.window, "window", history.DefaultWindow, "сколько последних сообщений уходит модели дословно (механизм window); у диалогов с rag.cite без этого флага — 12")
	flag.IntVar(&o.keep, "keep-tools", history.DefaultKeepToolRunes, "до скольких символов сокращать ответы инструментов прошлых ходов (механизм compact)")
	flag.IntVar(&o.limit, "context-limit", defaultContextLimit, "свой лимит контекста в токенах; 0 — не проверять")
	flag.StringVar(&o.mcpServer, "mcp-server", "", "бинарник MCP-сервера источников (механизм mcp); пусто — рядом с приложением, в PATH или сборка из исходников")
	flag.StringVar(&o.overflow, "on-overflow", agent.OverflowFail, "что делать при переполнении: fail — не отправлять, trim — выбрасывать старые ходы, off — отправить как есть")
	flag.BoolVar(&o.report, "report", false, "прогнать испытания на живой модели и записать отчёт вместо запуска сервера")
	flag.StringVar(&o.trials, "trials", "all", "какие испытания гонять с -report: «all», «1,6», «И-2»")
	flag.StringVar(&o.reportOut, "report-out", filepath.Join("examples", "report.md"), "куда записать отчёт -report")
	flag.StringVar(&o.factsServer, "facts-server", feed.DefaultServer, "адрес демона «Интересных фактов» (animals-mcp -http, механизм trivia и раздел интерфейса); по умолчанию TRIVIA_SERVER, иначе этот; пусто — выключить. Токен — MCP_TOKEN")
	flag.StringVar(&o.mcpConfig, "mcp-config", "", "конфигурация реестра MCP-серверов (см. mcp-servers.example.json); пусто — MCP_CONFIG, иначе встроенная: sources, daemon, notes")
	flag.StringVar(&o.kb, "kb", "", "база знаний (собирается командой go run ./cmd/kb index); пусто — KB_DB, иначе <data>/kb.db")
	flag.StringVar(&o.embedURL, "embedder", "", "адрес эмбеддера (OpenAI-совместимый /v1/embeddings: сайдкар embedder/, Ollama, облако); пусто — EMBED_BASE_URL, иначе http://127.0.0.1:8777")
	flag.StringVar(&o.provider, "llm", "", "модель приложения: cloud — DeepSeek (DEEPSEEK_API_KEY), local — свой сервер Ollama/LM Studio/llama.cpp (LOCAL_LLM_BASE_URL, LOCAL_LLM_MODEL), без облака; пусто — LLM_PROVIDER, иначе cloud")
	flag.StringVar(&o.compare, "compare", "", "облачная сторона для сравнения RAG в окне «База знаний» (cloud; нужен DEEPSEEK_API_KEY); ходы диалогов облака не касаются")
	flag.Parse()
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "facts-server":
			o.factsSet = true
		case "window":
			o.windowSet = true
		case "context-limit":
			o.limitSet = true
		case "on-overflow":
			o.overflowSet = true
		}
	})
	return o
}

func main() {
	o := parseFlags()
	enableUTF8Console()
	loadEnvFiles()
	// v28: учёт сети — куда процесс ходит: эта машина, локальная сеть,
	// интернет. Ставится до первого HTTP-клиента.
	audit := netaudit.Install()

	switch o.overflow {
	case agent.OverflowFail, agent.OverflowTrim, agent.OverflowOff:
	default:
		fail(fmt.Errorf("неизвестный режим -on-overflow=%q; допустимы fail, trim, off", o.overflow))
	}
	registry := features.Catalog()
	defaults, err := registry.ParsePreset(o.preset, o.featureSpec, registry.Defaults())
	if err != nil {
		fail(err)
	}
	if err := registry.Validate(defaults); err != nil {
		fail(err)
	}

	// v27: модель — облачная (DeepSeek) или локальная (свой сервер); в
	// локальном режиме облачный ключ не нужен и не читается.
	cloud, err := newCloudSide(o.compare)
	if err != nil {
		fail(err)
	}
	pname, err := providerName(o.provider)
	if err != nil {
		fail(err)
	}
	prov, err := newProvider(pname)
	if err != nil {
		fail(err)
	}
	model := prov.Model
	overflow := o.overflow
	if prov.Name == ProviderLocal && !o.overflowSet {
		// Окно локальной модели — 16 тыс. токенов, а не миллион: длинный
		// диалог в него не влезает, старые ходы выбрасываются.
		overflow = agent.OverflowTrim
	}

	runner := agent.Runner{
		LLM: prov.LLM, Model: model, Temperature: 0,
		ContextLimit: prov.contextLimit(o.limit, o.limitSet), OnOverflow: overflow, Calibration: &tokens.Calibration{},
	}
	if o.report {
		if err := runReport(o, registry, defaults, runner, model); err != nil {
			fail(err)
		}
		return
	}
	a, err := wire(o, registry, defaults, runner, o.data, "")
	if err != nil {
		fail(err)
	}
	manager, people, compile, guide, local := a.Manager, a.People, a.Compile, a.Guide, a.Local
	loaded, problems := manager.Load()
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "предупреждение: файл диалога пропущен: "+p.Error())
	}

	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		fail(fmt.Errorf("встроенный фронтенд не читается: %w", err))
	}
	meta := map[string]any{"model": model, "window": o.window, "contextLimit": o.limit}
	for _, m := range []map[string]any{persona.Meta(), charter.Meta()} {
		for k, v := range m {
			meta[k] = v
		}
	}
	exts := append(people.Extension(), compile.Extension(manager)...)
	exts = append(exts, guide.Extension()...)
	exts = append(exts, a.Sources.Extension()...)
	exts = append(exts, a.Feed.Extension()...)
	exts = append(exts, a.Pipes.Extension()...)
	exts = append(exts, a.Hub.Extension()...)
	exts = append(exts, a.KB.Extension()...)
	// v26: окно «Локальная модель» — сервер, модели, проверки, вопрос.
	localCfg := localllm.FromEnv()
	exts = append(exts, localapi.Extension(&localapi.API{Config: localCfg})...)
	meta["local"] = localCfg
	meta["provider"] = prov.meta()
	// v28: вкладка «Локально и облако» и учёт сети.
	labelHosts(audit, prov, cloud, o.embedder().BaseURL)
	cmp := compareAPI(a.KB, prov, cloud, audit, o.kbQuestions())
	exts = append(exts, compareapi.Extension(cmp)...)
	if cloud != nil {
		meta["provider"].(map[string]any)["compare"] = cloud.Model
	}
	exts = append(exts, taskapi.Extension(manager)...)
	handler := server.New(manager, static, meta, exts...)

	listener, err := net.Listen("tcp", o.addr)
	if err != nil {
		fail(fmt.Errorf("не удалось занять %s: %w", o.addr, err))
	}
	url := "http://" + listener.Addr().String()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	fmt.Println("AnimalGuide — справочник по животным, с которым разговаривают")
	fmt.Println("  интерфейс:  " + url)
	fmt.Println("  модель:     " + prov.Label + providerTail(prov, runner))
	fmt.Println("  источники:  " + strings.Join(local.Names(), ", "))
	fmt.Printf("  диалоги:    %s (загружено: %d)\n", manager.DisplayDir(), loaded)
	fmt.Println("  свод:       " + guide.Store.DisplayPath(invariants.GuideID))
	fmt.Println("  механизмы:  " + defaults.String())
	fmt.Println("  факты:      " + factsLine(a.Feed.Remote))
	fmt.Println("  база:       " + kbLine(a.KB))
	fmt.Println("  локальная:  " + localCfg.Model + " @ " + localCfg.BaseURL + " (окно «Локальная модель»)")
	if cloud != nil {
		fmt.Println("  сравнение: облачная · " + cloud.Model + " — только вкладка «Локально и облако» окна «База знаний»")
	}
	fmt.Println("  сеть:       учёт запросов — кнопка «Сеть» и /api/net")
	if defaults.On(features.MCP) {
		fmt.Println("  MCP:        включён для новых диалогов; сервер запустится при первом ходе")
	}
	fmt.Println("  остановить: Ctrl+C")

	errs := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errs <- err
		}
	}()
	if o.open {
		openBrowser(url)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	select {
	case err := <-errs:
		fail(err)
	case <-signals:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	a.Close()
	fmt.Println("Остановлено. Диалоги остались в " + manager.DisplayDir())
}

// factsLine — строка о демоне фактов для стартового вывода. Проверка
// короткая: старт не ждёт демон дольше двух секунд, а приложение работает
// и без него.
func factsLine(r *feed.Remote) string {
	if !r.Configured() {
		return "выключены (-facts-server \"\")"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st := r.Status(ctx)
	switch st.Conn {
	case feed.ConnOK:
		line := r.Server() + " (подключён"
		if st.Version != "" {
			line += ", версия " + st.Version
		}
		return line + ")"
	case feed.ConnDenied:
		return r.Server() + " (отверг токен — " + st.Hint + ")"
	}
	why := "не отвечает — " + st.Hint
	if ctx.Err() != nil {
		why = "не ответил за 2 с — " + st.Hint
	}
	return r.Server() + " (" + why + ")"
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "Ошибка: "+err.Error())
	os.Exit(1)
}
