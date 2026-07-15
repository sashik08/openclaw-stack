// Package system — тонкая обёртка над docker/docker compose и определением ОС.
// Здесь же логика автоустановки Docker для Ubuntu/Debian/CentOS.
package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// HasDocker проверяет, установлен ли docker и запущен ли демон.
func HasDocker() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	// docker info падает, если демон не поднят — это тоже «нет докера» для нас.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "info").Run() == nil
}

// ComposeCmd возвращает базовую команду compose: сначала пробуем плагин
// `docker compose`, затем legacy-бинарь `docker-compose`. Проба версии
// ограничена ctx, чтобы зависший docker-CLI не заблокировал установку.
func ComposeCmd(ctx context.Context) ([]string, error) {
	if exec.CommandContext(ctx, "docker", "compose", "version").Run() == nil {
		return []string{"docker", "compose"}, nil
	}
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return []string{"docker-compose"}, nil
	}
	return nil, fmt.Errorf("не найден ни `docker compose`, ни `docker-compose`")
}

// Distro грубо определяет семейство дистрибутива по /etc/os-release.
func Distro() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	text := strings.ToLower(string(data))
	switch {
	case strings.Contains(text, "ubuntu"):
		return "ubuntu"
	case strings.Contains(text, "debian"):
		return "debian"
	case strings.Contains(text, "centos"), strings.Contains(text, "rhel"),
		strings.Contains(text, "rocky"), strings.Contains(text, "almalinux"),
		strings.Contains(text, "fedora"):
		return "rhel"
	default:
		return "unknown"
	}
}

// InstallDocker ставит Docker Engine + compose plugin через официальный
// convenience-скрипт get.docker.com (поддерживает Ubuntu/Debian/CentOS).
// Требует root; run — колбэк для потокового вывода лога.
func InstallDocker(ctx context.Context, log func(string)) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("автоустановка Docker поддерживается только на Linux, у вас %s", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("для установки Docker нужны права root — запустите установщик через sudo")
	}
	log(fmt.Sprintf("Определён дистрибутив: %s. Устанавливаю Docker через get.docker.com…", Distro()))

	// Скачиваем и исполняем официальный скрипт.
	script := "curl -fsSL https://get.docker.com -o /tmp/get-docker.sh && sh /tmp/get-docker.sh"
	if err := runShell(ctx, log, script); err != nil {
		return fmt.Errorf("не удалось установить Docker: %w", err)
	}
	// Включаем и запускаем демон (на CentOS после установки он выключен).
	_ = runShell(ctx, log, "systemctl enable --now docker || true")

	if !HasDocker() {
		return fmt.Errorf("Docker установлен, но демон не отвечает — проверьте `systemctl status docker`")
	}
	log("Docker успешно установлен и запущен.")
	return nil
}

// ---- Работа с контейнерами (для монитора и дашборда) ----

// ContainerRunning возвращает true, если контейнер существует и в статусе running.
func ContainerRunning(ctx context.Context, name string) (bool, error) {
	out, err := output(ctx, "docker", "inspect", "-f", "{{.State.Running}}", name)
	if err != nil {
		return false, err // контейнера нет
	}
	return strings.TrimSpace(out) == "true", nil
}

// RestartContainer перезапускает контейнер по имени.
func RestartContainer(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, "docker", "restart", name).Run()
}

// ContainerLogs возвращает последние n строк логов контейнера.
func ContainerLogs(ctx context.Context, name string, n int) (string, error) {
	return output(ctx, "docker", "logs", "--tail", strconv.Itoa(n), name)
}

// Stats — снимок ресурсов одного контейнера.
type Stats struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemUsageMB float64 `json:"mem_usage_mb"`
	MemPercent float64 `json:"mem_percent"`
}

// ContainerStats снимает CPU/RAM контейнера через `docker stats --no-stream`.
func ContainerStats(ctx context.Context, name string) (Stats, error) {
	// Формат: "12.34%|256.3MiB / 2GiB|6.25%"
	out, err := output(ctx, "docker", "stats", "--no-stream", "--format",
		"{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}", name)
	if err != nil {
		return Stats{}, err
	}
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) != 3 {
		return Stats{}, fmt.Errorf("неожиданный формат docker stats: %q", out)
	}
	s := Stats{
		CPUPercent: parsePercent(parts[0]),
		MemPercent: parsePercent(parts[2]),
	}
	// "256.3MiB / 2GiB" -> берём левую часть, переводим в МБ
	if usage := strings.SplitN(parts[1], "/", 2); len(usage) > 0 {
		s.MemUsageMB = parseSizeMB(strings.TrimSpace(usage[0]))
	}
	return s, nil
}

func parsePercent(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	return v
}

func parseSizeMB(s string) float64 {
	s = strings.TrimSpace(s)
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "GiB"):
		mult, s = 1024, strings.TrimSuffix(s, "GiB")
	case strings.HasSuffix(s, "MiB"):
		mult, s = 1, strings.TrimSuffix(s, "MiB")
	case strings.HasSuffix(s, "KiB"):
		mult, s = 1.0/1024, strings.TrimSuffix(s, "KiB")
	case strings.HasSuffix(s, "B"):
		mult, s = 1.0/(1024*1024), strings.TrimSuffix(s, "B")
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v * mult
}

// ---- вспомогательное ----

func runShell(ctx context.Context, log func(string), script string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Stdout = writerFunc(log)
	cmd.Stderr = writerFunc(log)
	return cmd.Run()
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// writerFunc адаптирует функцию логирования под io.Writer построчно.
type writerFunc func(string)

func (f writerFunc) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			f(line)
		}
	}
	return len(p), nil
}
