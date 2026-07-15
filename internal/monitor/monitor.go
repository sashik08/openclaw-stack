// Package monitor — супервизор сервисов: раз в 120 секунд проверяет здоровье
// каждого сервиса, при падении перезапускает контейнер, ведёт историю рестартов
// и кольцевой буфер логов. Дашборд читает состояние из общего Store.
package monitor

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"deploystack/internal/config"
	"deploystack/internal/system"
)

// CheckInterval — период health-check по требованию (каждые 120 секунд).
const CheckInterval = 120 * time.Second

// opTimeout — предел на один вызов docker/HTTP. Без него зависший демон Docker
// (`docker inspect`/`stats`/`restart` могут блокироваться неограниченно)
// заморозил бы весь последовательный цикл проверок и отключил авторестарт.
const opTimeout = 30 * time.Second

// Health — состояние здоровья сервиса.
type Health string

const (
	HealthUp      Health = "up"
	HealthDown    Health = "down"
	HealthRestart Health = "restarting"
	HealthUnknown Health = "unknown"
)

// Kind — способ проверки здоровья сервиса.
type Kind string

const (
	KindHTTP      Kind = "http"      // GET по URL, ждём 2xx/3xx
	KindTelegram  Kind = "telegram"  // Telegram getMe + контейнер OpenClaw жив
	KindContainer Kind = "container" // просто State.Running
)

// Spec — описание одного отслеживаемого сервиса.
type Spec struct {
	Name      string `json:"name"`      // человекочитаемое имя
	Container string `json:"container"` // имя docker-контейнера для рестарта/статистики
	Kind      Kind   `json:"kind"`
	HealthURL string `json:"health_url,omitempty"` // для KindHTTP
}

// ServiceState — текущее состояние сервиса для отдачи в дашборд.
type ServiceState struct {
	Spec         Spec         `json:"spec"`
	Health       Health       `json:"health"`
	LastCheck    time.Time    `json:"last_check"`
	LastError    string       `json:"last_error,omitempty"`
	RestartCount int          `json:"restart_count"`
	Stats        system.Stats `json:"stats"`
}

// RestartEvent — запись в истории рестартов.
type RestartEvent struct {
	Service string    `json:"service"`
	Time    time.Time `json:"time"`
	Reason  string    `json:"reason"`
	Success bool      `json:"success"`
}

// Store — потокобезопасное хранилище состояния (общий для монитора и дашборда).
type Store struct {
	mu       sync.RWMutex
	services map[string]*ServiceState
	order    []string
	history  []RestartEvent // история рестартов (новые в конце)
	logs     []string       // кольцевой буфер последних логов
	maxLogs  int
}

// NewStore инициализирует хранилище по списку сервисов.
func NewStore(specs []Spec) *Store {
	s := &Store{
		services: make(map[string]*ServiceState),
		maxLogs:  300,
	}
	for _, sp := range specs {
		s.services[sp.Name] = &ServiceState{Spec: sp, Health: HealthUnknown}
		s.order = append(s.order, sp.Name)
	}
	return s
}

// Logf пишет событие в stdout, в лог-файл (через стандартный logger) и в буфер.
func (s *Store) Logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	log.Print(line) // уходит и в stdout, и в файл (см. настройку в cmd)
	stamped := time.Now().Format("2006-01-02 15:04:05") + " " + line
	s.mu.Lock()
	s.logs = append(s.logs, stamped)
	if len(s.logs) > s.maxLogs {
		s.logs = s.logs[len(s.logs)-s.maxLogs:]
	}
	s.mu.Unlock()
}

// Snapshot — согласованный слепок состояния для JSON-ответа дашборда.
type Snapshot struct {
	Services []ServiceState `json:"services"`
	History  []RestartEvent `json:"history"`
	Logs     []string       `json:"logs"`
}

// Snapshot возвращает копию текущего состояния.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{}
	for _, name := range s.order {
		out.Services = append(out.Services, *s.services[name])
	}
	// История и логи — последние 50 и 100 записей соответственно.
	out.History = tail(s.history, 50)
	out.Logs = tailStr(s.logs, 100)
	return out
}

// FindContainer возвращает имя контейнера по имени сервиса (для ручного рестарта).
func (s *Store) FindContainer(service string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.services[service]
	if !ok {
		return "", false
	}
	return st.Spec.Container, true
}

// Monitor гоняет цикл проверок.
type Monitor struct {
	store  *Store
	cfg    *config.Config
	client *http.Client
	specs  []Spec

	// checkRunning — проверка «контейнер запущен». Вынесена в поле как шов
	// для тестов; по умолчанию — реальный system.ContainerRunning.
	checkRunning func(ctx context.Context, name string) (bool, error)
}

// New создаёт монитор.
func New(store *Store, cfg *config.Config, specs []Spec) *Monitor {
	return &Monitor{
		store:        store,
		cfg:          cfg,
		specs:        specs,
		client:       &http.Client{Timeout: 8 * time.Second},
		checkRunning: system.ContainerRunning,
	}
}

// Run запускает бесконечный цикл проверок (до отмены ctx). Первый прогон —
// сразу, далее каждые CheckInterval.
func (m *Monitor) Run(ctx context.Context) {
	m.store.Logf("Монитор запущен: интервал проверок %s, сервисов: %d", CheckInterval, len(m.specs))
	ticker := time.NewTicker(CheckInterval)
	defer ticker.Stop()
	m.checkAll(ctx)
	for {
		select {
		case <-ctx.Done():
			m.store.Logf("Монитор остановлен")
			return
		case <-ticker.C:
			m.checkAll(ctx)
		}
	}
}

func (m *Monitor) checkAll(ctx context.Context) {
	for i := range m.specs {
		m.checkOne(ctx, m.specs[i])
	}
}

// checkOne проверяет один сервис и при неисправности перезапускает контейнер.
func (m *Monitor) checkOne(ctx context.Context, sp Spec) {
	// Каждый внешний вызов ограничен собственным таймаутом, чтобы зависание
	// одного не блокировало проверку остальных сервисов и следующий тик.
	probeCtx, cancel := context.WithTimeout(ctx, opTimeout)
	healthy, reason := m.probe(probeCtx, sp)
	cancel()

	// Обновляем ресурсы (даже если сервис лежит — покажем что есть).
	statsCtx, cancel := context.WithTimeout(ctx, opTimeout)
	stats, _ := system.ContainerStats(statsCtx, sp.Container)
	cancel()

	m.store.mu.Lock()
	st := m.store.services[sp.Name]
	st.LastCheck = time.Now()
	st.Stats = stats
	m.store.mu.Unlock()

	if healthy {
		m.setHealth(sp.Name, HealthUp, "")
		return
	}

	// Нездоров — логируем и перезапускаем контейнер.
	m.setHealth(sp.Name, HealthRestart, reason)
	m.store.Logf("[%s] нездоров (%s) — перезапуск контейнера %s", sp.Name, reason, sp.Container)
	restartCtx, cancel := context.WithTimeout(ctx, opTimeout)
	err := system.RestartContainer(restartCtx, sp.Container)
	cancel()
	ev := RestartEvent{Service: sp.Name, Time: time.Now(), Reason: reason, Success: err == nil}

	m.store.mu.Lock()
	m.store.history = append(m.store.history, ev)
	st = m.store.services[sp.Name]
	st.RestartCount++
	m.store.mu.Unlock()

	if err != nil {
		m.setHealth(sp.Name, HealthDown, "рестарт не удался: "+err.Error())
		m.store.Logf("[%s] РЕСТАРТ НЕ УДАЛСЯ: %v", sp.Name, err)
		return
	}
	m.store.Logf("[%s] контейнер перезапущен", sp.Name)
}

// Restart — ручной перезапуск по кнопке из дашборда.
func (m *Monitor) Restart(ctx context.Context, service string) error {
	container, ok := m.store.FindContainer(service)
	if !ok {
		return fmt.Errorf("неизвестный сервис %q", service)
	}
	m.setHealth(service, HealthRestart, "ручной перезапуск")
	m.store.Logf("[%s] ручной перезапуск контейнера %s", service, container)
	err := system.RestartContainer(ctx, container)
	m.store.mu.Lock()
	m.store.history = append(m.store.history, RestartEvent{
		Service: service, Time: time.Now(), Reason: "ручной перезапуск", Success: err == nil,
	})
	if st := m.store.services[service]; st != nil {
		st.RestartCount++
	}
	m.store.mu.Unlock()
	return err
}

func (m *Monitor) setHealth(name string, h Health, errMsg string) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	if st := m.store.services[name]; st != nil {
		st.Health = h
		st.LastError = errMsg
	}
}

// probe выполняет проверку здоровья соответствующего типа.
// Возвращает (здоров, причина-если-нет).
func (m *Monitor) probe(ctx context.Context, sp Spec) (bool, string) {
	// Базово: контейнер вообще запущен?
	running, err := m.checkRunning(ctx, sp.Container)
	if err != nil || !running {
		return false, "контейнер не запущен"
	}

	switch sp.Kind {
	case KindContainer:
		return true, ""

	case KindHTTP:
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sp.HealthURL, nil)
		resp, err := m.client.Do(req)
		if err != nil {
			return false, "HTTP-проба не прошла: " + err.Error()
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return true, ""
		}
		return false, fmt.Sprintf("HTTP %d от %s", resp.StatusCode, sp.HealthURL)

	case KindTelegram:
		// Проверяем валидность бота через getMe. Контейнер OpenClaw уже жив (см. выше).
		if m.cfg.BotToken == "" {
			return false, "токен бота не задан"
		}
		url := "https://api.telegram.org/bot" + m.cfg.BotToken + "/getMe"
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := m.client.Do(req)
		if err != nil {
			return false, "Telegram API недоступен: " + err.Error()
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode == 200 {
			return true, ""
		}
		return false, fmt.Sprintf("Telegram getMe вернул HTTP %d (проверьте токен)", resp.StatusCode)
	}
	return false, "неизвестный тип проверки"
}

// DefaultSpecs строит список сервисов для мониторинга из конфига.
// Telegram моделируется как логический сервис: OpenClaw ведёт канал сам,
// поэтому его «контейнер» — тот же openclaw-gateway.
func DefaultSpecs(cfg *config.Config) []Spec {
	return []Spec{
		{
			Name:      "OpenClaw",
			Container: "openclaw-gateway",
			Kind:      KindHTTP,
			HealthURL: fmt.Sprintf("http://127.0.0.1:%d/healthz", config.OpenClawPort),
		},
		{
			Name:      "OmniRoute",
			Container: "omniroute",
			Kind:      KindHTTP,
			HealthURL: fmt.Sprintf("http://127.0.0.1:%d/", config.OmniRoutePort),
		},
		{
			Name:      "Telegram-бот",
			Container: "openclaw-gateway", // канал живёт внутри OpenClaw
			Kind:      KindTelegram,
		},
	}
}

func tail[T any](s []T, n int) []T {
	if len(s) <= n {
		out := make([]T, len(s))
		copy(out, s)
		return out
	}
	out := make([]T, n)
	copy(out, s[len(s)-n:])
	return out
}

func tailStr(s []string, n int) []string { return tail(s, n) }
