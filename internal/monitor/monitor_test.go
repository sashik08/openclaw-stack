package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"deploystack/internal/config"
)

// newTestMonitor собирает монитор с подменённой проверкой «контейнер запущен».
func newTestMonitor(t *testing.T, running bool, specs []Spec) (*Monitor, *Store) {
	t.Helper()
	store := NewStore(specs)
	m := New(store, config.Defaults(), specs)
	m.checkRunning = func(context.Context, string) (bool, error) { return running, nil }
	return m, store
}

func TestProbeHTTPHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sp := Spec{Name: "svc", Container: "c", Kind: KindHTTP, HealthURL: srv.URL}
	m, _ := newTestMonitor(t, true, []Spec{sp})
	if ok, reason := m.probe(context.Background(), sp); !ok {
		t.Fatalf("ожидали здоров, получили: %s", reason)
	}
}

func TestProbeHTTPUnhealthyOn500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sp := Spec{Name: "svc", Container: "c", Kind: KindHTTP, HealthURL: srv.URL}
	m, _ := newTestMonitor(t, true, []Spec{sp})
	if ok, _ := m.probe(context.Background(), sp); ok {
		t.Fatal("HTTP 500 должен считаться нездоровым")
	}
}

func TestProbeContainerNotRunning(t *testing.T) {
	sp := Spec{Name: "svc", Container: "c", Kind: KindHTTP, HealthURL: "http://127.0.0.1:1"}
	m, _ := newTestMonitor(t, false, []Spec{sp}) // контейнер «не запущен»
	ok, reason := m.probe(context.Background(), sp)
	if ok || reason != "контейнер не запущен" {
		t.Fatalf("ожидали (false, 'контейнер не запущен'), получили (%v, %q)", ok, reason)
	}
}

func TestStoreLogRingBuffer(t *testing.T) {
	s := NewStore(nil)
	s.maxLogs = 5
	for i := 0; i < 20; i++ {
		s.Logf("строка %d", i)
	}
	logs := s.Snapshot().Logs
	if len(logs) != 5 {
		t.Fatalf("ожидали 5 строк в буфере, получили %d", len(logs))
	}
}

func TestStoreFindContainer(t *testing.T) {
	s := NewStore([]Spec{{Name: "OpenClaw", Container: "openclaw-gateway", Kind: KindHTTP}})
	if c, ok := s.FindContainer("OpenClaw"); !ok || c != "openclaw-gateway" {
		t.Fatalf("FindContainer вернул (%q,%v)", c, ok)
	}
	if _, ok := s.FindContainer("нет-такого"); ok {
		t.Fatal("для несуществующего сервиса ожидали ok=false")
	}
}

func TestManualRestartRecordsHistory(t *testing.T) {
	specs := []Spec{{Name: "OpenClaw", Container: "openclaw-gateway", Kind: KindHTTP}}
	m, store := newTestMonitor(t, true, specs)

	// docker в тест-окружении нет → рестарт вернёт ошибку, но история должна
	// зафиксироваться с Success=false, а счётчик — увеличиться.
	_ = m.Restart(context.Background(), "OpenClaw")

	snap := store.Snapshot()
	if len(snap.History) != 1 {
		t.Fatalf("ожидали 1 запись истории, получили %d", len(snap.History))
	}
	if snap.History[0].Service != "OpenClaw" || snap.History[0].Reason != "ручной перезапуск" {
		t.Errorf("неверная запись истории: %+v", snap.History[0])
	}
	if snap.Services[0].RestartCount != 1 {
		t.Errorf("ожидали RestartCount=1, получили %d", snap.Services[0].RestartCount)
	}
}

func TestRestartUnknownService(t *testing.T) {
	m, _ := newTestMonitor(t, true, []Spec{{Name: "A", Container: "a"}})
	if err := m.Restart(context.Background(), "нет"); err == nil {
		t.Fatal("ожидали ошибку для неизвестного сервиса")
	}
}

func TestDefaultSpecs(t *testing.T) {
	specs := DefaultSpecs(config.Defaults())
	if len(specs) != 3 {
		t.Fatalf("ожидали 3 сервиса, получили %d", len(specs))
	}
	var kinds []Kind
	for _, s := range specs {
		kinds = append(kinds, s.Kind)
	}
	// Должны быть два HTTP и один Telegram.
	if kinds[0] != KindHTTP || kinds[2] != KindTelegram {
		t.Errorf("неожиданный набор типов: %v", kinds)
	}
}
