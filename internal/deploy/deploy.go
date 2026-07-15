// Package deploy — оркестрация установки: проверка/установка Docker, скачивание
// конфигов из GitHub, генерация .env, запуск docker compose, автоподключение
// бесплатных моделей OmniRoute и открытие браузера с дашбордом.
package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"deploystack/internal/config"
	"deploystack/internal/system"
)

// Файлы, которые тянем из репозитория (относительно RepoRawURL).
var configFiles = []string{"docker-compose.yml"}

// Run выполняет полный цикл установки. log — колбэк для потокового вывода.
func Run(ctx context.Context, cfg *config.Config, log func(string)) error {
	// 1. Docker
	if system.HasDocker() {
		log("✓ Docker уже установлен")
	} else {
		log("Docker не найден — устанавливаю…")
		if err := system.InstallDocker(ctx, log); err != nil {
			return err
		}
	}

	// 2. Каталог установки + скачивание конфигов
	if err := os.MkdirAll(cfg.InstallDir, 0o755); err != nil {
		return fmt.Errorf("не удалось создать каталог %s: %w", cfg.InstallDir, err)
	}
	log("Скачиваю конфигурационные файлы из репозитория…")
	if err := downloadConfigs(ctx, cfg, log); err != nil {
		return err
	}

	// 3. Генерация .env с секретами и токеном бота
	envPath := filepath.Join(cfg.InstallDir, ".env")
	if err := os.WriteFile(envPath, []byte(cfg.RenderEnv()), 0o600); err != nil {
		return fmt.Errorf("не удалось записать .env: %w", err)
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("не удалось сохранить config.json: %w", err)
	}
	log("✓ Конфигурация сгенерирована (.env, config.json)")

	// 4. Запуск стека в фоне
	log("Запускаю контейнеры (docker compose up -d)…")
	if err := composeUp(ctx, cfg, log); err != nil {
		return err
	}

	// 5. Регистрация Telegram-канала в OpenClaw
	log("Подключаю Telegram-канал к OpenClaw…")
	if err := addTelegramChannel(ctx, cfg, log); err != nil {
		// Не фатально: канал можно добавить позже, стек уже работает.
		log("⚠ Не удалось автоматически добавить Telegram-канал: " + err.Error())
	}

	// 6. Автоподключение бесплатных моделей OmniRoute
	log("Активирую бесплатные модели OmniRoute…")
	if err := bootstrapFreeModels(ctx, cfg, log); err != nil {
		log("⚠ Автоподключение бесплатных моделей не завершилось: " + err.Error())
	}

	log("✓ Стек развёрнут и запущен в фоне")
	return nil
}

// downloadConfigs качает файлы из RepoRawURL в каталог установки.
func downloadConfigs(ctx context.Context, cfg *config.Config, log func(string)) error {
	client := &http.Client{Timeout: 30 * time.Second}
	for _, name := range configFiles {
		url := cfg.RepoRawURL + "/" + name
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("скачивание %s: %w", name, err)
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("скачивание %s: сервер вернул HTTP %d", name, resp.StatusCode)
		}
		dst := filepath.Join(cfg.InstallDir, name)
		f, err := os.Create(dst)
		if err != nil {
			resp.Body.Close()
			return fmt.Errorf("создание %s: %w", dst, err)
		}
		_, err = io.Copy(f, resp.Body)
		resp.Body.Close()
		f.Close()
		if err != nil {
			return fmt.Errorf("запись %s: %w", dst, err)
		}
		log("  ✓ " + name)
	}
	return nil
}

func composeUp(ctx context.Context, cfg *config.Config, log func(string)) error {
	base, err := system.ComposeCmd(ctx)
	if err != nil {
		return err
	}
	args := append(base[1:], "-f", filepath.Join(cfg.InstallDir, "docker-compose.yml"),
		"--env-file", filepath.Join(cfg.InstallDir, ".env"), "up", "-d")
	return runIn(ctx, cfg.InstallDir, log, base[0], args...)
}

// addTelegramChannel регистрирует бота в OpenClaw через CLI-сайдкар.
func addTelegramChannel(ctx context.Context, cfg *config.Config, log func(string)) error {
	base, err := system.ComposeCmd(ctx)
	if err != nil {
		return err
	}
	args := append(base[1:], "-f", filepath.Join(cfg.InstallDir, "docker-compose.yml"),
		"--env-file", filepath.Join(cfg.InstallDir, ".env"),
		"run", "--rm", "openclaw-cli",
		"channels", "add", "--channel", "telegram", "--token", cfg.BotToken)
	return runIn(ctx, cfg.InstallDir, log, base[0], args...)
}

// bootstrapFreeModels дожидается готовности OmniRoute и включает бесплатные пулы.
// OmniRoute поднимается с OMNIROUTE_ENABLE_FREE_MODELS=true; здесь мы лишь ждём,
// пока API ответит, чтобы установка завершалась «готовой к работе».
func bootstrapFreeModels(ctx context.Context, cfg *config.Config, log func(string)) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/", config.OmniRoutePort)
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				log("  ✓ OmniRoute отвечает, бесплатные модели активны")
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("OmniRoute не ответил за 90с")
}

// OpenBrowser пытается открыть дашборд в браузере (best-effort).
func OpenBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}

func runIn(ctx context.Context, dir string, log func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = lineWriter(log)
	cmd.Stderr = lineWriter(log)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("`%s %v` завершилась с ошибкой: %w", name, args, err)
	}
	return nil
}

type lineWriter func(string)

func (f lineWriter) Write(p []byte) (int, error) {
	f(string(p))
	return len(p), nil
}
