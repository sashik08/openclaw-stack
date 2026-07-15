// Команда deploystack — единый бинарь установщика и монитора.
//
// Режимы:
//
//	deploystack install   — (по умолчанию) интерактивная/флаговая установка стека
//	deploystack dashboard — запуск монитора и веб-дашборда (обычно в фоне)
//
// Пользователь запускает install.sh, который скачивает/собирает этот бинарь и
// вызывает `deploystack install`. Дашборд поднимается как фоновая служба.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"deploystack/internal/config"
	"deploystack/internal/dashboard"
	"deploystack/internal/deploy"
	"deploystack/internal/monitor"
)

func main() {
	// Первый позиционный аргумент (без ведущего дефиса) — это команда.
	// Собираем новый срез os.Args, чтобы не мутировать общий backing-массив.
	mode := "install"
	rest := os.Args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		mode = rest[0]
		rest = rest[1:]
	}
	os.Args = append([]string{os.Args[0]}, rest...)
	switch mode {
	case "install":
		runInstall()
	case "dashboard":
		runDashboard()
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q (доступно: install, dashboard)\n", mode)
		os.Exit(2)
	}
}

// ---- Установка ----

func runInstall() {
	cfg := config.Defaults()

	var target, botToken, dashUser, dashPass, publicHost string
	var noBrowser, yes bool
	flag.StringVar(&target, "target", "", "тип развёртывания: localhost|vps")
	flag.StringVar(&botToken, "bot-token", "", "токен Telegram-бота")
	flag.StringVar(&dashUser, "dashboard-user", "", "логин дашборда (по умолчанию admin)")
	flag.StringVar(&dashPass, "dashboard-pass", "", "пароль дашборда (по умолчанию генерируется)")
	flag.StringVar(&publicHost, "public-host", "", "белый IP/домен для VPS (для ссылок)")
	flag.IntVar(&cfg.DashboardPort, "dashboard-port", cfg.DashboardPort, "порт дашборда мониторинга")
	flag.BoolVar(&noBrowser, "no-browser", false, "не открывать браузер после установки")
	flag.BoolVar(&yes, "yes", false, "неинтерактивно: не спрашивать, брать значения из флагов")
	flag.Parse()

	// Применяем флаги.
	if target != "" {
		cfg.Target = config.Target(target)
	}
	if botToken != "" {
		cfg.BotToken = botToken
	}
	if dashUser != "" {
		cfg.DashboardUser = dashUser
	}
	if dashPass != "" {
		cfg.DashboardPass = dashPass
	}
	if publicHost != "" {
		cfg.PublicHost = publicHost
	}

	// Интерактивное меню (если не --yes и чего-то не хватает).
	interactive := !yes && isTTY()
	if interactive {
		promptInteractive(cfg)
	}
	// Пароль дашборда генерируем самостоятельным секретом, если не задан.
	if cfg.DashboardPass == "" {
		cfg.DashboardPass = config.RandSecret(12)
	}

	if err := cfg.Validate(); err != nil {
		fatal("Проверьте параметры: " + err.Error())
	}

	// Собственно установка.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := func(s string) { fmt.Println(s) }

	fmt.Println("\n=== Развёртывание OpenClaw Stack ===")
	if err := deploy.Run(ctx, cfg, log); err != nil {
		fatal("Установка прервана: " + err.Error())
	}

	// Поднимаем дашборд/монитор как фоновую службу.
	if err := startBackgroundDashboard(cfg); err != nil {
		log("⚠ Не удалось поднять дашборд в фоне автоматически: " + err.Error())
		log("  Запустите вручную: deploystack dashboard")
	}

	printCredentials(cfg)
	if !noBrowser && isTTY() {
		deploy.OpenBrowser(cfg.DashboardURL())
	}
}

// promptInteractive дозапрашивает недостающие поля через простое меню.
func promptInteractive(cfg *config.Config) {
	in := bufio.NewReader(os.Stdin)

	if cfg.Target == "" || (cfg.Target != config.TargetLocalhost && cfg.Target != config.TargetVPS) {
		fmt.Println("\nТип развёртывания:")
		fmt.Println("  1) localhost — всё на этой машине, дашборд только на 127.0.0.1")
		fmt.Println("  2) vps       — сервер, дашборд доступен по белому IP")
		switch strings.TrimSpace(ask(in, "Выберите [1/2] (по умолчанию 1): ")) {
		case "2", "vps":
			cfg.Target = config.TargetVPS
		default:
			cfg.Target = config.TargetLocalhost
		}
	}
	if cfg.Target == config.TargetVPS && cfg.PublicHost == "" {
		cfg.PublicHost = strings.TrimSpace(ask(in, "Белый IP или домен сервера (Enter — пропустить): "))
	}
	for cfg.BotToken == "" {
		cfg.BotToken = strings.TrimSpace(ask(in, "Токен Telegram-бота (от @BotFather): "))
		if cfg.BotToken == "" {
			fmt.Println("  Токен обязателен.")
		}
	}
	if v := strings.TrimSpace(ask(in, fmt.Sprintf("Логин дашборда (Enter — %s): ", cfg.DashboardUser))); v != "" {
		cfg.DashboardUser = v
	}
	if v := strings.TrimSpace(ask(in, "Пароль дашборда (Enter — сгенерировать): ")); v != "" {
		cfg.DashboardPass = v
	}
}

func ask(r *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	line, _ := r.ReadString('\n')
	return line
}

func printCredentials(cfg *config.Config) {
	fmt.Println("\n============================================")
	fmt.Println(" Готово! Стек работает и мониторится.")
	fmt.Println("============================================")
	fmt.Printf(" Дашборд мониторинга : %s\n", cfg.DashboardURL())
	fmt.Printf("   логин  : %s\n", cfg.DashboardUser)
	fmt.Printf("   пароль : %s\n", cfg.DashboardPass)
	fmt.Println("--------------------------------------------")
	fmt.Printf(" OpenClaw UI : http://%s:%d/\n", hostFor(cfg), config.OpenClawPort)
	fmt.Printf(" OmniRoute   : http://%s:%d/  (логин admin / %s)\n", hostFor(cfg), config.OmniRoutePort, cfg.OmniroutePassword)
	fmt.Println("============================================")
	fmt.Printf(" Конфиги и логи: %s\n\n", cfg.InstallDir)
}

func hostFor(cfg *config.Config) string {
	if cfg.Target == config.TargetVPS && cfg.PublicHost != "" {
		return cfg.PublicHost
	}
	return "localhost"
}

// startBackgroundDashboard пытается зарегистрировать systemd-службу; если
// systemd недоступен — запускает бинарь через nohup.
func startBackgroundDashboard(cfg *config.Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// Вариант 1: systemd (требует root).
	if _, err := exec.LookPath("systemctl"); err == nil && os.Geteuid() == 0 {
		unit := fmt.Sprintf(`[Unit]
Description=OpenClaw Stack Monitor
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=%s dashboard
Environment=OPENCLAW_STACK_DIR=%s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, self, cfg.InstallDir)
		path := "/etc/systemd/system/openclaw-stack-monitor.service"
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
			return err
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		return exec.Command("systemctl", "enable", "--now", "openclaw-stack-monitor").Run()
	}

	// Вариант 2: nohup в фон.
	logFile := filepath.Join(cfg.InstallDir, "monitor.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "dashboard")
	cmd.Env = append(os.Environ(), "OPENCLAW_STACK_DIR="+cfg.InstallDir)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // отвязать от терминала
	return cmd.Start()
}

// ---- Дашборд/монитор (фоновая служба) ----

func runDashboard() {
	dir := os.Getenv("OPENCLAW_STACK_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".openclaw-stack")
	}
	cfg, err := config.Load(dir)
	if err != nil {
		fatal("Не удалось прочитать конфиг из " + dir + ": " + err.Error())
	}

	// Лог в файл + stdout.
	setupLogging(filepath.Join(cfg.InstallDir, "monitor.log"))

	specs := monitor.DefaultSpecs(cfg)
	store := monitor.NewStore(specs)
	mon := monitor.New(store, cfg, specs)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go mon.Run(ctx) // проверки каждые 120с

	srv := &http.Server{Addr: cfg.DashboardBind(), Handler: dashboard.New(cfg, store, mon).Handler()}
	go func() {
		store.Logf("Дашборд слушает %s", cfg.DashboardBind())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			store.Logf("Дашборд остановлен с ошибкой: %v", err)
		}
	}()

	<-ctx.Done()
	srv.Close()
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "✗ "+msg)
	os.Exit(1)
}

func isTTY() bool {
	fi, _ := os.Stdin.Stat()
	return fi != nil && (fi.Mode()&os.ModeCharDevice) != 0
}
