package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexS8332/AnimalGuide_Task28/internal/compare"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/compareapi"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/kbapi"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/llm"
	"github.com/AlexS8332/AnimalGuide_Task28/internal/netaudit"
)

// defaultCompareJSON — отчёт kb compare, который показывает вкладка.
var defaultCompareJSON = filepath.Join("examples", "compare", "report.json")

// cloudSide — облачный генератор только для сравнения (v28, -compare
// cloud). Ключ читается до того, как локальный режим уберёт его из
// окружения, и живёт только в этом клиенте: ходы диалогов его не видят.
type cloudSide struct {
	LLM   llm.Chatter
	Model string
	Host  string
}

func newCloudSide(flagValue string) (*cloudSide, error) {
	switch strings.ToLower(strings.TrimSpace(flagValue)) {
	case "", "none", "off":
		return nil, nil
	case "cloud", "deepseek":
	default:
		return nil, fmt.Errorf("неизвестное значение -compare=%q; допустимо cloud", flagValue)
	}
	key := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("-compare cloud: нужен DEEPSEEK_API_KEY — облачная сторона сравнения")
	}
	model := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = llm.DefaultModel
	}
	c := llm.NewClient(key, os.Getenv("DEEPSEEK_BASE_URL"))
	return &cloudSide{LLM: c, Model: model, Host: hostOf(c.BaseURL)}, nil
}

// compareAPI — стороны вкладки: генератор приложения и, если задан,
// облачный. Поиск у них общий — Answerer базы знаний.
func compareAPI(kbAPI *kbapi.API, prov provider, cloud *cloudSide, audit *netaudit.Audit, questions string) *compareapi.API {
	api := &compareapi.API{Questions: questions, ReportPath: defaultCompareJSON, Net: audit}
	if kbAPI == nil || kbAPI.Answerer == nil {
		api.Why = "базы знаний нет — сравнивать не на чем"
		if kbAPI != nil && kbAPI.Why != "" {
			api.Why = kbAPI.Why
		}
		return api
	}
	main := compare.Side{Name: compare.Cloud, Label: prov.Label, Answerer: kbAPI.Answerer}
	if prov.Name == ProviderLocal {
		main.Name = compare.Local
	}
	api.Sides = append(api.Sides, main)
	if cloud != nil && prov.Name == ProviderLocal {
		a := *kbAPI.Answerer
		a.LLM, a.Model = cloud.LLM, cloud.Model
		api.Sides = append(api.Sides, compare.Side{Name: compare.Cloud, Label: "облачная · " + cloud.Model + " (только сравнение)", Answerer: &a})
	}
	return api
}

// labelHosts называет адреса в учёте сети.
func labelHosts(a *netaudit.Audit, prov provider, cloud *cloudSide, embedURL string) {
	if prov.Name == ProviderLocal {
		a.Label(hostOf(prov.BaseURL), "локальная модель")
	} else {
		a.Label(hostOf(prov.BaseURL), "облачная модель")
	}
	if cloud != nil {
		a.Label(cloud.Host, "облачная модель — только сравнение")
	}
	a.Label(hostOf(embedURL), "эмбеддер")
	a.Label("ru.wikipedia.org", "Википедия")
	a.Label("ru.wikipedia.org:443", "Википедия")
	a.Label("api.gbif.org", "GBIF")
	a.Label("api.gbif.org:443", "GBIF")
}

// hostOf — host:port из URL; без порта — хост, как его пишет Request.URL.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}
