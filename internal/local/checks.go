package local

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
)

// Уровни сложности запросов.
const (
	LevelSimple  = "простой"
	LevelMedium  = "средний"
	LevelComplex = "сложный"
)

// Check — один запрос к модели с проверкой ответа кодом. Проверка — не
// судья-модель: правило, которое видно в коде и не зависит от облака.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Level  string `json:"level"`
	What   string `json:"what"` // что проверяет запрос
	System string `json:"system,omitempty"`
	Prompt string `json:"prompt"`
	// Expect — словами, какой ответ засчитывается.
	Expect    string `json:"expect"`
	MaxTokens int    `json:"maxTokens"`
	tools     []llm.ToolDef
	verify    func(llm.Response) (bool, string)
}

// Fragments — дословные фрагменты статей базы знаний (снимок ru-Википедии,
// CC BY-SA 4.0): сложный запрос считает только по ним.
const Fragments = `[manul/Описание] Длина головы и туловища составляет от 46 до 65 см, длина хвоста — от 21 до 31 см, масса составляет 2—5 кг.
[lynx/Описание] Масса взрослых самцов от 15 до 25 кг, максимум — 35 кг (по другим данным, максимальный вес старых самцов — 32 кг); самки весят в среднем 20 кг (от 7 до 21,5 кг).`

var kbSearchTool = llm.NewToolDef("kb_search",
	"Поиск фрагментов в базе знаний справочника о хищных Евразии. Возвращает фрагменты статей с chunk_id.",
	json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"поисковый запрос по-русски"},"k":{"type":"integer","description":"сколько фрагментов вернуть, 1–10"}},"required":["query"]}`))

// Checks — пять запросов от простого к сложному: факт одной строкой, редкий факт,
// строгий JSON, вызов инструмента, рассуждение по фрагментам с арифметикой.
func Checks() []Check {
	return []Check{
		{
			ID: "fact", Title: "Факт одной строкой", Level: LevelSimple,
			What:      "знание и краткость: короткий вопрос — короткий ответ",
			Prompt:    "Как по-латыни называется бурый медведь? Ответь только латинским названием.",
			Expect:    "Ursus arctos",
			MaxTokens: 64,
			verify:    latinCheck("Ursus arctos"),
		},
		{
			ID: "rare", Title: "Редкий факт", Level: LevelMedium,
			What: "предел знаний малой модели: вид, о котором в обучающих данных мало текста — " +
				"ровно поэтому справочнику нужны источники",
			Prompt:    "Как по-латыни называется манул? Ответь только латинским названием.",
			Expect:    "Otocolobus manul",
			MaxTokens: 64,
			verify:    latinCheck("Otocolobus manul"),
		},
		{
			ID: "json", Title: "Строгий JSON", Level: LevelMedium,
			What:   "формат: ответ разбирается кодом без правок",
			System: "Ты отвечаешь только валидным JSON без пояснений и без markdown.",
			Prompt: `Опиши ирбиса (снежного барса) объектом JSON с полями: "name" — русское название, "latin" — латинское название, ` +
				`"family" — семейство по-русски, "countries" — массив из трёх стран, где он обитает.`,
			Expect:    `JSON: latin = Panthera uncia, family — кошачьи, countries — 3 страны`,
			MaxTokens: 256,
			verify:    verifyJSON,
		},
		{
			ID: "tool", Title: "Вызов инструмента", Level: LevelMedium,
			What: "агентность: модель сама решает позвать поиск по базе и заполняет аргументы",
			System: "Ты — справочник о хищных Евразии. Факты бери только из базы знаний: " +
				"на вопрос о животном сначала вызови kb_search.",
			Prompt:    "Сколько весит росомаха?",
			Expect:    "вызов kb_search с запросом про росомаху",
			MaxTokens: 256,
			tools:     []llm.ToolDef{kbSearchTool},
			verify: func(r llm.Response) (bool, string) {
				if !r.HasToolCalls() {
					return false, "инструмент не вызван — ответ из памяти модели"
				}
				c := r.Message.ToolCalls[0]
				if c.Function.Name != "kb_search" {
					return false, "вызван не тот инструмент: " + c.Function.Name
				}
				var args struct {
					Query string `json:"query"`
				}
				if err := json.Unmarshal([]byte(c.Function.Arguments), &args); err != nil {
					return false, "аргументы — не JSON: " + err.Error()
				}
				if !strings.Contains(strings.ToLower(args.Query), "росомах") {
					return false, "запрос без росомахи: " + args.Query
				}
				return true, fmt.Sprintf("kb_search(%q)", args.Query)
			},
		},
		{
			ID: "reason", Title: "Рассуждение по фрагментам", Level: LevelComplex,
			What: "понимание текста и арифметика в несколько шагов: найти нужные числа, " +
				"не перепутать максимум со средним, посчитать",
			System: "Отвечай по-русски и только по приведённым фрагментам.",
			Prompt: "Фрагменты:\n" + Fragments + "\n\nВопрос: во сколько раз рекордная масса самца рыси (значение, " +
				"названное в тексте словом «максимум», а не «по другим данным») больше верхней границы массы манула? " +
				"Коротко покажи расчёт, а последней строкой напиши «Ответ: N».",
			Expect:    "35 кг / 5 кг → «Ответ: 7»",
			MaxTokens: 512,
			verify: func(r llm.Response) (bool, string) {
				m := answerRe.FindAllStringSubmatch(r.Message.Content, -1)
				if len(m) == 0 {
					return false, "нет строки «Ответ: N»"
				}
				got := strings.ReplaceAll(m[len(m)-1][1], ",", ".")
				switch got {
				case "7", "7.0":
					return true, "35 / 5 = 7"
				}
				return false, "ответ " + got + ", ожидалось 7"
			},
		},
	}
}

// latinCheck — в ответе латинское название, и ответ короткий.
func latinCheck(want string) func(llm.Response) (bool, string) {
	return func(r llm.Response) (bool, string) {
		got := strings.TrimSpace(r.Message.Content)
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			return false, "нет «" + want + "», ответ: " + got
		}
		if len([]rune(got)) > 60 {
			return false, "верно, но не одной строкой"
		}
		return true, "латынь верная, ответ короткий"
	}
}

var answerRe = regexp.MustCompile(`(?i)ответ\s*[:：]\s*\**\s*([0-9]+(?:[.,][0-9]+)?)`)

func verifyJSON(r llm.Response) (bool, string) {
	raw := strings.TrimSpace(r.Message.Content)
	fenced := strings.HasPrefix(raw, "```")
	if fenced {
		raw = strings.TrimPrefix(strings.TrimPrefix(raw, "```json"), "```")
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "```"))
	}
	var v struct {
		Name      string   `json:"name"`
		Latin     string   `json:"latin"`
		Family    string   `json:"family"`
		Countries []string `json:"countries"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return false, "не JSON: " + err.Error()
	}
	var bad []string
	if !strings.EqualFold(strings.TrimSpace(v.Latin), "Panthera uncia") {
		bad = append(bad, "latin = "+v.Latin)
	}
	if !strings.Contains(strings.ToLower(v.Family), "кошач") {
		bad = append(bad, "family = "+v.Family)
	}
	if len(v.Countries) != 3 {
		bad = append(bad, fmt.Sprintf("стран %d", len(v.Countries)))
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	note := "JSON разобран, поля верные"
	if fenced {
		note += " (в обёртке ```)"
	}
	return true, note
}

// Result — один запрос и его итог.
type Result struct {
	ID       string    `json:"id,omitempty"`
	Title    string    `json:"title,omitempty"`
	Level    string    `json:"level,omitempty"`
	Model    string    `json:"model"`
	Prompt   string    `json:"prompt"`
	Answer   string    `json:"answer"`
	Calls    []string  `json:"calls,omitempty"` // вызовы инструментов: имя(аргументы)
	Finish   string    `json:"finish,omitempty"`
	OK       *bool     `json:"ok,omitempty"` // нет проверки — nil
	Verdict  string    `json:"verdict,omitempty"`
	Error    string    `json:"error,omitempty"`
	TookMS   int64     `json:"tookMs"`
	Usage    llm.Usage `json:"usage"`
	TokPerS  float64   `json:"tokPerS"` // токенов ответа в секунду, с учётом чтения запроса
	Started  time.Time `json:"started"`
	Expected string    `json:"expected,omitempty"`
}

// Run выполняет проверку.
func Run(ctx context.Context, c llm.Chatter, model string, ch Check) Result {
	var msgs []llm.Message
	if ch.System != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: ch.System})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: ch.Prompt})
	res := call(ctx, c, llm.Request{Model: model, Messages: msgs, Tools: ch.tools, MaxTokens: ch.MaxTokens})
	res.ID, res.Title, res.Level, res.Prompt, res.Expected = ch.ID, ch.Title, ch.Level, ch.Prompt, ch.Expect
	if res.Error == "" && ch.verify != nil {
		ok, why := ch.verify(res.resp)
		res.OK, res.Verdict = &ok, why
	}
	return res.Result
}

// Ask — свободный вопрос без проверки.
func Ask(ctx context.Context, c llm.Chatter, model, system, prompt string, maxTokens int) Result {
	var msgs []llm.Message
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: system})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: prompt})
	res := call(ctx, c, llm.Request{Model: model, Messages: msgs, MaxTokens: maxTokens})
	res.Prompt = prompt
	return res.Result
}

// Find — проверка по id.
func Find(id string) (Check, bool) {
	for _, c := range Checks() {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

type called struct {
	Result
	resp llm.Response
}

func call(ctx context.Context, c llm.Chatter, req llm.Request) called {
	started := time.Now()
	resp, err := c.Chat(ctx, req)
	took := time.Since(started)
	out := called{Result: Result{Model: req.Model, Started: started, TookMS: took.Milliseconds()}, resp: resp}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if resp.Model != "" {
		out.Model = resp.Model
	}
	out.Answer = resp.Message.Content
	out.Finish = resp.FinishReason
	out.Usage = resp.Usage
	for _, tc := range resp.Message.ToolCalls {
		out.Calls = append(out.Calls, tc.Function.Name+"("+tc.Function.Arguments+")")
	}
	if s := took.Seconds(); s > 0 && resp.Usage.Completion > 0 {
		out.TokPerS = float64(resp.Usage.Completion) / s
	}
	return out
}

// HasTools — запрос несёт описания инструментов.
func (c Check) HasTools() bool { return len(c.tools) > 0 }
