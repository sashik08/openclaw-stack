# OpenClaw Stack — автоматизированное развёртывание

Одна команда разворачивает на Linux (Ubuntu / Debian / CentOS) связку сервисов в Docker Compose
и поднимает над ними самовосстанавливающийся мониторинг с веб-дашбордом. От пользователя нужно
только выбрать `localhost` / `VPS` и ввести токен Telegram-бота — всё остальное (проверка и
установка Docker, скачивание конфигов, генерация секретов, запуск контейнеров, настройка
мониторинга и открытие браузера) делается автоматически.

| Сервис | Роль | Порт |
|---|---|---|
| **OmniRoute** | Бесплатный AI-шлюз: один endpoint `/v1`, десятки бесплатных моделей | `20128` |
| **OpenClaw** | Агент; ведёт Telegram-канал сам | `18789` |
| **Telegram-бот** | Не отдельный контейнер, а канал внутри OpenClaw (см. [«Про Telegram»](#про-telegram)) | — |
| **deploystack dashboard** | Наш монитор + веб-дашборд | `8088` |

## Содержание

- [Быстрый старт](#быстрый-старт)
- [Требования](#требования)
- [Флаги установщика](#флаги-установщика)
- [Что получает пользователь](#что-получает-пользователь)
- [Архитектура](#архитектура)
- [Структура репозитория](#структура-репозитория)
- [Мониторинг и самовосстановление](#мониторинг-и-самовосстановление)
- [Веб-дашборд](#веб-дашборд)
- [Управление службой](#управление-службой)
- [Про Telegram](#про-telegram)
- [Разработка и тесты](#разработка-и-тесты)
- [Замечания по боевому использованию](#замечания-по-боевому-использованию)

## Быстрый старт

> Пошаговое руководство (подготовка репозитория, localhost/VPS, эксплуатация, удаление,
> диагностика) — в [DEPLOYMENT.md](DEPLOYMENT.md).

Интерактивно (спросит тип развёртывания и токен бота):

```bash
curl -fsSL https://raw.githubusercontent.com/sashik08/openclaw-stack/main/install.sh | sudo bash
```

Неинтерактивно (для автоматизации / CI):

```bash
curl -fsSL https://raw.githubusercontent.com/sashik08/openclaw-stack/main/install.sh | sudo bash -s -- \
  --target vps --public-host 203.0.113.10 \
  --bot-token 123456:ABC... \
  --dashboard-user admin --dashboard-pass 'S3cret!' \
  --no-browser
```

## Требования

- Linux: Ubuntu / Debian / CentOS (RHEL / Rocky / Alma / Fedora).
- Права `root` (нужны для установки Docker и регистрации systemd-службы) — отсюда `sudo`.
- Доступ в интернет (скачать Docker, образы, конфиги).
- Docker ставить заранее **не нужно** — установщик поставит его сам через `get.docker.com`, если не найдёт.
- Токен Telegram-бота от [@BotFather](https://t.me/BotFather).

## Флаги установщика

`deploystack install [флаги]` (их же можно передать через `bash -s --`):

| Флаг | Значение по умолчанию | Описание |
|---|---|---|
| `--target` | спросит / `localhost` | Тип развёртывания: `localhost` или `vps` |
| `--bot-token` | спросит | Токен Telegram-бота (обязателен) |
| `--public-host` | — | Белый IP / домен для VPS (для генерации ссылок и `NEXT_PUBLIC_BASE_URL`) |
| `--dashboard-user` | `admin` | Логин веб-дашборда |
| `--dashboard-pass` | генерируется | Пароль веб-дашборда |
| `--dashboard-port` | `8088` | Порт веб-дашборда мониторинга |
| `--no-browser` | `false` | Не открывать браузер после установки |
| `--yes` | `false` | Неинтерактивно: не задавать вопросов, брать значения из флагов |

Секреты сервисов (`JWT_SECRET`, `API_KEY_SECRET`, `STORAGE_ENCRYPTION_KEY`,
`OPENCLAW_GATEWAY_TOKEN`, пароль OmniRoute) генерируются автоматически — вводить их не нужно.

## Что получает пользователь

После установки в консоль печатаются учётные данные и ссылки:

```
Дашборд мониторинга : http://localhost:8088    (логин / пароль)
OpenClaw UI         : http://localhost:18789/
OmniRoute           : http://localhost:20128/  (admin / <сгенерированный пароль>)
```

Дашборд открывается в браузере автоматически (кроме `--no-browser` и не-TTY окружений, например
при запуске через pipe в `bash`). Всё, что записано на диск, лежит в `~/.openclaw-stack/`
(для root — `/root/.openclaw-stack/`):

| Файл | Что внутри |
|---|---|
| `config.json` | Полный конфиг + секреты (права `0600`) |
| `.env` | Переменные окружения для compose (права `0600`) |
| `docker-compose.yml` | Скачанный из репозитория |
| `monitor.log` | Лог событий монитора (старты, ошибки, рестарты) |

## Архитектура

```
                    ┌─────────────────────────────────────────────┐
  один бинарь       │  install.sh  →  deploystack install          │
  deploystack       │    ├─ проверка/установка Docker (get.docker) │
                    │    ├─ скачивание docker-compose.yml из GitHub │
                    │    ├─ генерация .env (секреты + токен бота)   │
                    │    ├─ docker compose up -d                    │
                    │    ├─ openclaw-cli channels add --telegram    │
                    │    └─ регистрация фоновой службы (systemd)    │
                    └─────────────────────────────────────────────┘
                                        │
          ┌─────────────────────────────┼──────────────────────────┐
          ▼                             ▼                          ▼
   ┌─────────────┐              ┌─────────────────┐        ┌────────────────┐
   │  OmniRoute  │◀── /v1 ──────│ openclaw-gateway │        │  deploystack   │
   │  :20128     │   (модели)   │  :18789          │        │  dashboard     │
   └─────────────┘              │  + Telegram      │◀─ health checks ──┘
   docker compose               └─────────────────┘   каждые 120с + рестарт
                                                             │  :8088 (Basic Auth)
                                                             ▼  веб-дашборд
```

Монитор (`deploystack dashboard`) — тот же бинарь, запущенный как systemd-служба
`openclaw-stack-monitor` (или через `nohup`, если systemd недоступен). Он работает на хосте и
имеет доступ к docker CLI, поэтому может проверять здоровье, снимать статистику и перезапускать
контейнеры.

## Структура репозитория

| Путь | Назначение |
|---|---|
| `install.sh` | Bootstrap: ставит Go при необходимости, собирает/качает бинарь, запускает установку |
| `cmd/deploystack/main.go` | CLI: разбор флагов, интерактивное меню, режимы `install` / `dashboard` |
| `cmd/deploystack/logging.go` | Логирование одновременно в stdout и в файл |
| `internal/config` | Модель конфига, генерация секретов, рендер `.env`, save/load JSON |
| `internal/system` | Обёртки над docker/compose, автоустановка Docker, `docker stats`/`restart`/`logs` |
| `internal/deploy` | Оркестрация установки: Docker → конфиги → `.env` → compose up → Telegram → браузер |
| `internal/monitor` | Супервизор: health-checks каждые 120с, авторестарт, история, буфер логов |
| `internal/dashboard` | HTTP-дашборд с Basic Auth, API статуса/рестарта, встроенный `index.html` |
| `deploy/docker-compose.yml` | Оркестрация контейнеров (хранится в GitHub, тянется установщиком) |
| `deploy/env.example` | Образец переменных окружения (для ручной отладки) |

## Мониторинг и самовосстановление

- **Health-checks каждые 120 секунд** (`monitor.CheckInterval`) для каждого сервиса:
  - OpenClaw — HTTP `GET /healthz`;
  - OmniRoute — HTTP `GET /`;
  - Telegram — `getMe` к Telegram API + живость контейнера OpenClaw.
- **Авторестарт**: при неудачной пробе `docker restart <container>`, событие пишется в историю.
- **Таймауты**: каждый вызов docker/HTTP ограничен 30с — зависший демон Docker не блокирует
  проверку остальных сервисов и следующий тик.
- **Двойная страховка**: у контейнеров в compose стоит `restart: always` + собственный
  `healthcheck` (детерминированный уровень Docker), поверх — активный супервизор с логикой,
  историей и ручными кнопками.
- **Логи** пишутся одновременно в stdout и в `~/.openclaw-stack/monitor.log`.

## Веб-дашборд

- Защита Basic Auth (логин/пароль задаются при установке), constant-time сравнение.
- Реал-тайм (опрос раз в 5с): статус каждого сервиса, CPU / RAM, число рестартов, ошибки.
- История рестартов и последние логи.
- Кнопка «Перезагрузить» на каждом сервисе (`POST /api/restart?service=…`).
- localhost — слушает `127.0.0.1`; VPS — `0.0.0.0` (порты за фаервол / туннель / HTTPS-прокси
  выносите самостоятельно).

## Управление службой

```bash
# статус и логи монитора
systemctl status openclaw-stack-monitor
journalctl -u openclaw-stack-monitor -f
tail -f ~/.openclaw-stack/monitor.log

# перезапуск / остановка монитора
systemctl restart openclaw-stack-monitor
systemctl stop openclaw-stack-monitor

# управление самими сервисами (из каталога установки)
cd ~/.openclaw-stack
docker compose --env-file .env ps
docker compose --env-file .env logs -f openclaw-gateway
docker compose --env-file .env restart omniroute
```

## Про Telegram

OpenClaw подключает Telegram как **канал** внутри своего контейнера — отдельного образа
«Telegram-бота» не существует. Поэтому в мониторинге Telegram — логический сервис: его здоровье
= валидный `getMe` + живой `openclaw-gateway`, а «перезапуск Telegram» = рестарт OpenClaw.
Список сервисов задаётся в `monitor.DefaultSpecs` — если появится отдельный бот-контейнер,
достаточно добавить туда ещё один `Spec`.

## Разработка и тесты

Проект без внешних зависимостей (только stdlib), Go 1.22+.

```bash
go build ./...                 # сборка всего
go vet ./...                   # статанализ
go test -race ./...            # тесты (config, system, monitor, dashboard)

go build -o deploystack ./cmd/deploystack
./deploystack install --yes --target localhost --bot-token X --dashboard-pass Y --no-browser
./deploystack dashboard        # монитор + дашборд (читает ~/.openclaw-stack/config.json)
```

Покрытие тестами (все зелёные под `-race`):

| Пакет | Что проверяется |
|---|---|
| `config` | Валидация, рендер `.env` (секреты + токен, VPS-ветка), bind/URL, уникальность секретов, save↔load |
| `system` | Парсинг `docker stats` (`parsePercent`, `parseSizeMB`: MiB/GiB/KiB/B) |
| `monitor` | HTTP-проба (healthy/500/контейнер не запущен), кольцевой буфер логов, история и счётчик рестартов, `DefaultSpecs` |
| `dashboard` | Basic Auth (401 без/с неверным паролем), `/api/status` (JSON), `/api/restart` (только POST), отдача `index.html` |

Порядок, в котором собирался проект: `config` → `system` → `monitor` → `dashboard` → `deploy`
→ `cmd` → `docker-compose.yml` + `install.sh`.

## Замечания по боевому использованию

- Репозиторий в `install.sh` и `config.Defaults().RepoRawURL` уже настроен на
  `sashik08/openclaw-stack` (форкнули под другой аккаунт — поменяйте на свой; переопределяется и
  на лету через env `OPENCLAW_STACK_REPO` / `OPENCLAW_STACK_RAW`).
  Для готовых бинарей `install.sh` ждёт их в GitHub Releases (`deploystack-<os>-<arch>`); если
  релиза нет — соберёт из исходников (нужен Go, ставится автоматически).
- Точные теги образов (`ghcr.io/openclaw/openclaw:latest`, `ghcr.io/diegosouzapw/omniroute:latest`)
  сверьте с релизами апстрима перед публикацией.
- Флаг автоподключения бесплатных моделей `OMNIROUTE_ENABLE_FREE_MODELS` апстримом явно не
  задокументирован — если OmniRoute его не поддерживает, бесплатные пулы включаются через его
  дашборд / API; логика ожидания готовности уже в `deploy.bootstrapFreeModels`.
- Для VPS обязательно закройте порты `18789` / `20128` фаерволом и вынесите дашборд за
  HTTPS-прокси (тогда в `.env` имеет смысл `AUTH_COOKIE_SECURE=true`).
