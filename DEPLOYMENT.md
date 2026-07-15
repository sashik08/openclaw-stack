# Инструкция по развёртыванию OpenClaw Stack

Документ описывает развёртывание от и до для двух ролей:

- **Часть A — мейнтейнер**: разово подготовить репозиторий и опубликовать релиз, чтобы
  `install.sh` работал одной командой.
- **Часть B — пользователь**: развернуть стек на своей машине / VPS.
- **Часть C — эксплуатация**: проверка, управление, обновление, удаление, диагностика.

Если репозиторий уже опубликован мейнтейнером — переходите сразу к [части B](#часть-b--развёртывание-пользователем).

---

## Часть A — подготовка репозитория (мейнтейнер, разово)

### A1. Репозиторий проекта

Проект уже настроен на **`sashik08/openclaw-stack`** — специально менять ничего не нужно.
Репозиторий прописан в двух местах:

1. `install.sh` — переменные `REPO` и `RAW` в начале файла.
2. `internal/config/config.go` — поле `RepoRawURL` в `Defaults()` (база raw-ссылок, откуда
   тянется `docker-compose.yml`).

Если форкаете под другой аккаунт `<owner>/<repo>` — замените там же (либо переопределите на лету
через env `OPENCLAW_STACK_REPO` / `OPENCLAW_STACK_RAW`, не трогая код):

```bash
sed -i 's#sashik08/openclaw-stack#<owner>/<repo>#g' install.sh internal/config/config.go
```

### A2. Сверить теги образов апстрима

В `deploy/docker-compose.yml` и `internal/config/config.go` (`RenderEnv`) используются:

- `ghcr.io/openclaw/openclaw:latest`
- `ghcr.io/diegosouzapw/omniroute:latest`

Перед публикацией сверьте актуальные теги с релизами апстрима и при необходимости зафиксируйте
конкретные версии вместо `latest`.

### A3. Проверить сборку и тесты локально

```bash
go vet ./...
go test -race ./...
go build ./cmd/deploystack
```

Всё должно быть зелёным (CI повторит это на push — см. `.github/workflows/ci.yml`).

### A4. Залить файлы в репозиторий

В корне репозитория обязательно должны лежать (по этим путям их ждут `install.sh` и установщик):

- `install.sh`
- `deploy/docker-compose.yml`
- исходники `cmd/`, `internal/`, `go.mod` (нужны для фолбэка «сборка из исходников»).

```bash
git add .
git commit -m "OpenClaw Stack deploy tooling"
git push origin main
```

### A5. Выпустить релиз с бинарями

`install.sh` сперва пытается скачать готовый бинарь
`…/releases/latest/download/deploystack-linux-<arch>`. Их собирает `.github/workflows/release.yml`
по тегу `v*`:

```bash
git tag v1.0.0
git push origin v1.0.0
```

Workflow соберёт `deploystack-linux-amd64` и `deploystack-linux-arm64` (+ `.sha256`) и приложит
их к GitHub Release. После этого установка у пользователя не требует Go — бинарь просто
скачивается. Если релиза нет, `install.sh` соберёт бинарь из исходников (Go поставит сам).

### A6. Проверить публичность файлов

```bash
# должны отдаваться без авторизации:
curl -fsSLI https://raw.githubusercontent.com/<owner>/<repo>/main/install.sh
curl -fsSLI https://raw.githubusercontent.com/<owner>/<repo>/main/deploy/docker-compose.yml
curl -fsSLI https://github.com/<owner>/<repo>/releases/latest/download/deploystack-linux-amd64
```

---

## Часть B — развёртывание пользователем

### Предварительно

- ОС: Ubuntu / Debian / CentOS (RHEL / Rocky / Alma / Fedora).
- Права `root` (нужны для установки Docker и systemd-службы) → команда идёт через `sudo`.
- Токен Telegram-бота от [@BotFather](https://t.me/BotFather) (команда `/newbot`).
- Docker ставить заранее **не нужно** — установщик поставит сам, если не найдёт.

Замените `<owner>/<repo>` в командах ниже на реальный репозиторий из части A.

### Сценарий 1 — localhost (своя машина), интерактивно

```bash
curl -fsSL https://raw.githubusercontent.com/<owner>/<repo>/main/install.sh | sudo bash
```

Установщик спросит:

1. **Тип развёртывания** → выберите `1` (localhost).
2. **Токен Telegram-бота** → вставьте токен от @BotFather.
3. **Логин/пароль дашборда** → Enter, чтобы принять `admin` и сгенерированный пароль
   (или введите свои).

По завершении в консоли появятся ссылки и учётные данные, а браузер откроется на дашборде
`http://localhost:8088`.

### Сценарий 2 — VPS (сервер), неинтерактивно

На сервере обычно нет TTY/браузера — задайте всё флагами и отключите открытие браузера:

```bash
curl -fsSL https://raw.githubusercontent.com/<owner>/<repo>/main/install.sh | sudo bash -s -- \
  --target vps \
  --public-host 203.0.113.10 \
  --bot-token 123456:ABC-DEF... \
  --dashboard-user admin \
  --dashboard-pass 'СложныйПароль!' \
  --no-browser
```

На VPS дашборд слушает `0.0.0.0:8088`. **Обязательно** ограничьте доступ (см. [C4](#c4-безопасность-vps)).

### Флаги установщика

| Флаг | По умолчанию | Описание |
|---|---|---|
| `--target` | спросит / `localhost` | `localhost` или `vps` |
| `--bot-token` | спросит | Токен Telegram-бота (обязателен) |
| `--public-host` | — | Белый IP / домен для VPS (для ссылок) |
| `--dashboard-user` | `admin` | Логин дашборда |
| `--dashboard-pass` | генерируется | Пароль дашборда |
| `--dashboard-port` | `8088` | Порт дашборда |
| `--no-browser` | `false` | Не открывать браузер |
| `--yes` | `false` | Неинтерактивно (значения только из флагов) |

---

## Часть C — эксплуатация

### C1. Проверка после установки

```bash
# контейнеры подняты и healthy
cd ~/.openclaw-stack           # для root: /root/.openclaw-stack
docker compose --env-file .env ps

# служба монитора работает
systemctl status openclaw-stack-monitor

# дашборд отвечает (401 без авторизации — это нормально)
curl -si http://localhost:8088/ | head -n 1
```

Ожидаемо: три сервиса (`omniroute`, `openclaw-gateway`; Telegram — канал внутри OpenClaw),
дашборд на `:8088`, OpenClaw UI на `:18789`, OmniRoute на `:20128`.

### C2. Учётные данные и файлы

Всё лежит в `~/.openclaw-stack/`:

| Файл | Что внутри |
|---|---|
| `config.json` | Конфиг + все секреты, включая пароль дашборда и OmniRoute (`0600`) |
| `.env` | Переменные окружения для compose (`0600`) |
| `docker-compose.yml` | Скачанный из репозитория |
| `monitor.log` | Лог событий монитора |

Забыли пароль дашборда:

```bash
grep -E 'dashboard_(user|pass)|omniroute_password' ~/.openclaw-stack/config.json
```

### C3. Управление сервисами и монитором

```bash
# логи и статус монитора
journalctl -u openclaw-stack-monitor -f
tail -f ~/.openclaw-stack/monitor.log
systemctl restart openclaw-stack-monitor

# сами сервисы
cd ~/.openclaw-stack
docker compose --env-file .env logs -f openclaw-gateway
docker compose --env-file .env restart omniroute
```

Перезапускать сервисы можно и кнопками в веб-дашборде — там же статус, CPU/RAM, история
рестартов и последние логи в реальном времени.

### C4. Безопасность VPS

На VPS дашборд и сервисы доступны по сети — закройте их:

```bash
# отдать наружу только дашборд, остальное закрыть (пример ufw)
sudo ufw allow 8088/tcp
sudo ufw deny 18789/tcp
sudo ufw deny 20128/tcp
```

Лучше — вынести дашборд за HTTPS-обратный прокси (nginx/Caddy) и тогда в `~/.openclaw-stack/.env`
включить `AUTH_COOKIE_SECURE=true`, либо вообще не публиковать порт, а ходить через SSH-туннель:

```bash
ssh -L 8088:localhost:8088 user@203.0.113.10   # затем http://localhost:8088 на своей машине
```

### C5. Обновление

```bash
cd ~/.openclaw-stack
docker compose --env-file .env pull        # свежие образы OpenClaw/OmniRoute
docker compose --env-file .env up -d
```

Обновить сам установщик/монитор — перезапустить `install.sh` (конфиг и секреты в `config.json`
сохранятся) либо заменить бинарь `deploystack` новой сборкой и `systemctl restart
openclaw-stack-monitor`.

### C6. Полное удаление

```bash
# остановить монитор
sudo systemctl disable --now openclaw-stack-monitor
sudo rm -f /etc/systemd/system/openclaw-stack-monitor.service
sudo systemctl daemon-reload

# остановить и удалить контейнеры + тома (ВНИМАНИЕ: сотрёт данные OmniRoute/OpenClaw)
cd ~/.openclaw-stack
docker compose --env-file .env down -v

# удалить каталог установки (там секреты и логи)
rm -rf ~/.openclaw-stack
```

### C7. Диагностика частых проблем

| Симптом | Причина / решение |
|---|---|
| `для установки Docker нужны права root` | Запускайте через `sudo` (или `sudo bash`) |
| `Telegram getMe вернул HTTP 401` в дашборде | Неверный токен бота — исправьте `TELEGRAM_BOT_TOKEN` в `.env`, `docker compose up -d`, при необходимости заново `channels add` |
| Сервис постоянно `restarting` в дашборде | Смотрите `docker compose logs <service>` — обычно нехватка RAM (OpenClaw требует ≥ 2 ГБ) или занятый порт |
| Дашборд не открылся в браузере | Не-TTY/сервер без GUI — это норм, откройте ссылку из вывода вручную |
| `OmniRoute не ответил за 90с` при установке | Первый старт образа дольше — стек уже поднят, проверьте `docker compose ps` через минуту |
| Порт `8088`/`18789`/`20128` занят | Освободите порт или задайте другой `--dashboard-port`; порты сервисов правятся в `.env` |

---

Полное описание архитектуры, компонентов и разработки — в [README.md](README.md).
