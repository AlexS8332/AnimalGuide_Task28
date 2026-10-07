package netaudit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8777":       ClassLoopback,
		"[::1]:80":             ClassLoopback,
		"localhost:11434":      ClassLoopback,
		"192.168.1.243:11434":  ClassLAN,
		"10.0.0.5":             ClassLAN,
		"172.20.1.1:443":       ClassLAN,
		"mac-mini.local:11434": ClassLAN,
		"nas:8080":             ClassLAN,
		"api.deepseek.com":     ClassInternet,
		"ru.wikipedia.org:443": ClassInternet,
		"8.8.8.8:53":           ClassInternet,
		"172.32.0.1":           ClassInternet,
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAuditCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer srv.Close()
	a := New(nil)
	host := strings.TrimPrefix(srv.URL, "http://")
	a.Label(host, "эмбеддер")
	c := &http.Client{Transport: a}
	for i := 0; i < 3; i++ {
		resp, err := c.Get(srv.URL + "/health")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	// Ошибка соединения тоже считается — и как ошибка.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c.Get(dead.URL)

	s := a.Snapshot()
	if s.ByClass[ClassLoopback] != 4 || s.Internet != 0 || len(s.Hosts) != 2 {
		t.Fatalf("снимок: %+v", s)
	}
	if h := s.Hosts[0]; h.Host != host || h.Requests != 3 || h.Purpose != "эмбеддер" || h.Errors != 0 {
		t.Fatalf("адрес: %+v", h)
	}
	if s.Hosts[1].Errors != 1 {
		t.Fatalf("ошибка не посчитана: %+v", s.Hosts[1])
	}
	a.Reset()
	if s := a.Snapshot(); len(s.Hosts) != 0 || s.ByClass[ClassLAN] != 0 {
		t.Fatalf("после сброса: %+v", s)
	}
}

func TestInstall(t *testing.T) {
	prev := http.DefaultTransport
	defer func() { http.DefaultTransport = prev }()
	a := Install()
	if http.DefaultTransport != a {
		t.Fatal("DefaultTransport не подменён")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	resp, err := (&http.Client{}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if a.Snapshot().ByClass[ClassLoopback] != 1 {
		t.Fatal("клиент без своего транспорта прошёл мимо учёта")
	}
}

// Клиенты приложения не задают свой транспорт — иначе запрос прошёл бы мимо
// учёта, и «интернет 0» врал бы. Исключение — клиенты команд (cmd/), они
// работают вне приложения.
func TestNoCustomTransportsInApp(t *testing.T) {
	root := filepath.Join("..", "..")
	re := regexp.MustCompile(`http\.Client\{[^}]*Transport:`)
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "/cmd/") {
			return nil
		}
		raw, _ := os.ReadFile(path)
		if re.Match(raw) {
			t.Errorf("%s: http.Client со своим Transport — мимо учёта сети", path)
		}
		return nil
	})
}
