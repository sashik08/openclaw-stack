// Package config хранит конфигурацию развёртывания: тип цели (localhost/VPS),
// токен Telegram-бота, учётные данные дашборда и сгенерированные секреты.
// Конфиг сериализуется в JSON и кладётся рядом с проектом, чтобы монитор и
// дашборд, запущенные отдельным процессом, могли прочитать те же значения.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Target — тип развёртывания.
type Target string

const (
	TargetLocalhost Target = "localhost" // всё крутится локально, дашборд на 127.0.0.1
	TargetVPS       Target = "vps"       // сервер: дашборд слушает 0.0.0.0, секреты жёстче
)

// Config — полная конфигурация одной установки.
type Config struct {
	Target        Target `json:"target"`
	BotToken      string `json:"bot_token"`      // токен Telegram-бота (для OpenClaw-канала и health-check)
	DashboardUser string `json:"dashboard_user"` // логин для входа в дашборд мониторинга
	DashboardPass string `json:"dashboard_pass"` // пароль для входа в дашборд мониторинга
	DashboardPort int    `json:"dashboard_port"` // порт дашборда мониторинга (наш, не OmniRoute)
	PublicHost    string `json:"public_host"`    // белый IP/домен для VPS (для ссылок), пусто для localhost

	// Секреты сервисов — генерируются автоматически, пользователь их не вводит.
	OmniroutePassword    string `json:"omniroute_password"`     // INITIAL_PASSWORD для входа в OmniRoute
	OmnirouteJWTSecret   string `json:"omniroute_jwt_secret"`   // JWT_SECRET
	OmnirouteAPIKeySec   string `json:"omniroute_api_key_sec"`  // API_KEY_SECRET
	OmnirouteStorageKey  string `json:"omniroute_storage_key"`  // STORAGE_ENCRYPTION_KEY
	OpenClawGatewayToken string `json:"openclaw_gateway_token"` // OPENCLAW_GATEWAY_TOKEN

	InstallDir string `json:"install_dir"`  // куда скачаны docker-compose.yml/.env
	RepoRawURL string `json:"repo_raw_url"` // база raw-ссылок GitHub для скачивания конфигов
}

// Порты сервисов фиксированы образами (см. документацию проектов).
const (
	OpenClawPort  = 18789 // Control UI / gateway API OpenClaw
	OmniRoutePort = 20128 // Dashboard + API OmniRoute (/v1)
)

// Defaults возвращает конфиг с разумными значениями по умолчанию и
// свежесгенерированными секретами. Токен бота и учётку дашборда заполнит CLI.
func Defaults() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Target:               TargetLocalhost,
		DashboardUser:        "admin",
		DashboardPort:        8088,
		OmniroutePassword:    RandSecret(12),
		OmnirouteJWTSecret:   RandSecret(48),
		OmnirouteAPIKeySec:   RandSecret(32),
		OmnirouteStorageKey:  RandSecret(32),
		OpenClawGatewayToken: RandSecret(32),
		InstallDir:           filepath.Join(home, ".openclaw-stack"),
		RepoRawURL:           "https://raw.githubusercontent.com/sashik08/openclaw-stack/main/deploy",
	}
}

// Validate проверяет, что заполнено всё обязательное для запуска.
func (c *Config) Validate() error {
	var miss []string
	if strings.TrimSpace(c.BotToken) == "" {
		miss = append(miss, "токен Telegram-бота")
	}
	if strings.TrimSpace(c.DashboardUser) == "" {
		miss = append(miss, "логин дашборда")
	}
	if strings.TrimSpace(c.DashboardPass) == "" {
		miss = append(miss, "пароль дашборда")
	}
	if c.Target != TargetLocalhost && c.Target != TargetVPS {
		miss = append(miss, "тип развёртывания (localhost|vps)")
	}
	if len(miss) > 0 {
		return fmt.Errorf("не заполнено: %s", strings.Join(miss, ", "))
	}
	return nil
}

// DashboardBind возвращает адрес, на котором слушает дашборд мониторинга.
// Для VPS слушаем все интерфейсы, для localhost — только петлю.
func (c *Config) DashboardBind() string {
	if c.Target == TargetVPS {
		return fmt.Sprintf("0.0.0.0:%d", c.DashboardPort)
	}
	return fmt.Sprintf("127.0.0.1:%d", c.DashboardPort)
}

// DashboardURL — ссылка для открытия в браузере после установки.
func (c *Config) DashboardURL() string {
	host := "localhost"
	if c.Target == TargetVPS && c.PublicHost != "" {
		host = c.PublicHost
	}
	return fmt.Sprintf("http://%s:%d", host, c.DashboardPort)
}

// Path — путь к JSON-файлу конфига внутри каталога установки.
func (c *Config) Path() string { return filepath.Join(c.InstallDir, "config.json") }

// Save записывает конфиг в JSON с правами 0600 (внутри — секреты).
func (c *Config) Save() error {
	if err := os.MkdirAll(c.InstallDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path(), data, 0o600)
}

// Load читает конфиг из каталога установки.
func Load(installDir string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(installDir, "config.json"))
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// RenderEnv собирает содержимое файла .env для docker compose из конфига.
// Значения секретов подставляются сюда, чтобы образы поднялись сразу рабочими.
func (c *Config) RenderEnv() string {
	var b strings.Builder
	w := func(k, v string) { fmt.Fprintf(&b, "%s=%s\n", k, v) }

	b.WriteString("# Сгенерировано установщиком openclaw-stack. Не коммитить — тут секреты.\n\n")

	b.WriteString("# --- OpenClaw ---\n")
	w("OPENCLAW_IMAGE", "ghcr.io/openclaw/openclaw:latest")
	w("OPENCLAW_PORT", fmt.Sprint(OpenClawPort))
	w("OPENCLAW_GATEWAY_TOKEN", c.OpenClawGatewayToken)
	w("OPENCLAW_SANDBOX", "1")
	w("TELEGRAM_BOT_TOKEN", c.BotToken)

	b.WriteString("\n# --- OmniRoute ---\n")
	w("OMNIROUTE_PORT", fmt.Sprint(OmniRoutePort))
	w("INITIAL_PASSWORD", c.OmniroutePassword)
	w("JWT_SECRET", c.OmnirouteJWTSecret)
	w("API_KEY_SECRET", c.OmnirouteAPIKeySec)
	w("STORAGE_ENCRYPTION_KEY", c.OmnirouteStorageKey)
	if c.Target == TargetVPS {
		w("AUTH_COOKIE_SECURE", "false") // true только если поставите HTTPS-прокси
		if c.PublicHost != "" {
			w("NEXT_PUBLIC_BASE_URL", fmt.Sprintf("http://%s:%d", c.PublicHost, OmniRoutePort))
		}
	}
	// Автоподключение бесплатных моделей при первом старте — см. deploy/omniroute-bootstrap.
	w("OMNIROUTE_ENABLE_FREE_MODELS", "true")

	return b.String()
}

// RandSecret возвращает url-safe случайную строку примерно n символов.
func RandSecret(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand не должен падать; если упал — лучше явно, чем слабый секрет
		panic("не удалось получить случайные байты: " + err.Error())
	}
	s := base64.RawURLEncoding.EncodeToString(buf)
	if len(s) > n {
		s = s[:n]
	}
	return s
}
