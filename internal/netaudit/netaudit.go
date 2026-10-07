// Package netaudit — учёт исходящих HTTP-запросов процесса (v28): куда
// приложение ходит по сети. Обёртка над http.DefaultTransport, через
// который идут все клиенты приложения (модель, эмбеддер, источники,
// MCP по HTTP), считает запросы по адресам и делит адреса на классы:
// эта машина, локальная сеть, интернет.
//
// Так «RAG полностью локально» становится проверяемым: после вопросов
// справочной счётчик «интернет» остаётся нулём.
package netaudit

import (
	"net"
	"net/http"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Классы адресов.
const (
	ClassLoopback = "loopback" // эта машина
	ClassLAN      = "lan"      // частная сеть: 10/8, 172.16/12, 192.168/16, fc00::/7, link-local
	ClassInternet = "internet"
)

// Host — счётчик одного адреса.
type Host struct {
	Host     string    `json:"host"`
	Class    string    `json:"class"`
	Requests int       `json:"requests"`
	Errors   int       `json:"errors"`
	Last     time.Time `json:"last"`
	// Purpose — что за адрес, если его назвали (Label): «локальная модель»,
	// «эмбеддер», «Википедия».
	Purpose string `json:"purpose,omitempty"`
}

// Snapshot — состояние счётчиков.
type Snapshot struct {
	Since    time.Time      `json:"since"`
	Hosts    []Host         `json:"hosts"`
	ByClass  map[string]int `json:"byClass"`
	Internet int            `json:"internet"`
}

// Audit — счётчики и обёртка транспорта.
type Audit struct {
	mu     sync.Mutex
	since  time.Time
	hosts  map[string]*Host
	labels map[string]string
	next   http.RoundTripper
}

// New — учёт поверх транспорта next (nil — http.DefaultTransport).
func New(next http.RoundTripper) *Audit {
	if next == nil {
		next = http.DefaultTransport
	}
	return &Audit{since: time.Now(), hosts: map[string]*Host{}, labels: map[string]string{}, next: next}
}

// Install подменяет http.DefaultTransport учётом и возвращает его.
// Клиенты с собственным транспортом мимо учёта пройдут — в приложении
// таких нет (проверяет тест на исходники).
func Install() *Audit {
	a := New(http.DefaultTransport)
	http.DefaultTransport = a
	return a
}

// Label называет адрес host:port (или URL — берётся хост с портом).
func (a *Audit) Label(hostport, purpose string) {
	if hostport == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.labels[hostport] = purpose
	if h, ok := a.hosts[hostport]; ok {
		h.Purpose = purpose
	}
}

// RoundTrip — запрос через следующий транспорт, со счётом.
func (a *Audit) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := a.next.RoundTrip(r)
	a.count(r.URL.Host, err != nil)
	return resp, err
}

func (a *Audit) count(hostport string, failed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.hosts[hostport]
	if !ok {
		h = &Host{Host: hostport, Class: Classify(hostport), Purpose: a.labels[hostport]}
		a.hosts[hostport] = h
	}
	h.Requests++
	if failed {
		h.Errors++
	}
	h.Last = time.Now()
}

// Snapshot — копия счётчиков, адреса по убыванию запросов.
func (a *Audit) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Snapshot{Since: a.since, Hosts: []Host{}, ByClass: map[string]int{ClassLoopback: 0, ClassLAN: 0, ClassInternet: 0}}
	for _, h := range a.hosts {
		s.Hosts = append(s.Hosts, *h)
		s.ByClass[h.Class] += h.Requests
	}
	sort.Slice(s.Hosts, func(i, j int) bool {
		if s.Hosts[i].Requests != s.Hosts[j].Requests {
			return s.Hosts[i].Requests > s.Hosts[j].Requests
		}
		return s.Hosts[i].Host < s.Hosts[j].Host
	})
	s.Internet = s.ByClass[ClassInternet]
	return s
}

// Reset обнуляет счётчики (кнопка «с нуля» перед проверкой).
func (a *Audit) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hosts = map[string]*Host{}
	a.since = time.Now()
}

// Classify — класс адреса host[:port]. Имя без IP: localhost — эта
// машина, *.local и имя без точки — локальная сеть, остальное — интернет.
func Classify(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		switch {
		case ip.IsLoopback():
			return ClassLoopback
		case ip.IsPrivate(), ip.IsLinkLocalUnicast():
			return ClassLAN
		}
		return ClassInternet
	}
	switch {
	case host == "localhost":
		return ClassLoopback
	case len(host) > 6 && host[len(host)-6:] == ".local":
		return ClassLAN
	}
	for _, c := range host {
		if c == '.' {
			return ClassInternet
		}
	}
	return ClassLAN
}
