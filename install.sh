#!/usr/bin/env bash
# Единый установщик OpenClaw Stack.
# Одна команда для пользователя:
#   curl -fsSL https://raw.githubusercontent.com/sashik08/openclaw-stack/main/install.sh | bash
#
# Скрипт: ставит Go при отсутствии (нужен для сборки бинаря установщика),
# собирает/скачивает бинарь `deploystack` и запускает интерактивную установку.
# Всю тяжёлую логику (Docker, конфиги, контейнеры, мониторинг) делает Go-бинарь.
set -euo pipefail

REPO="${OPENCLAW_STACK_REPO:-https://github.com/sashik08/openclaw-stack}"
RAW="${OPENCLAW_STACK_RAW:-https://raw.githubusercontent.com/sashik08/openclaw-stack/main}"
BIN_DIR="/usr/local/bin"
BIN="deploystack"

log()  { printf '\033[36m[install]\033[0m %s\n' "$*"; }
err()  { printf '\033[31m[ошибка]\033[0m %s\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

need_root_hint() {
  if [ "$(id -u)" -ne 0 ]; then
    err "Для установки Docker и системной службы нужны права root."
    die "Перезапустите:  curl -fsSL $RAW/install.sh | sudo bash"
  fi
}

# Определяем платформу для готового бинаря (если публикуете релизы).
detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "неподдерживаемая архитектура: $arch" ;;
  esac
  echo "${os}-${arch}"
}

# Пытаемся скачать готовый бинарь из релизов; если не вышло — собираем из исходников.
fetch_or_build() {
  local platform url tmp
  platform="$(detect_platform)"
  url="$REPO/releases/latest/download/${BIN}-${platform}"
  tmp="$(mktemp)"

  log "Пробую скачать готовый бинарь: $url"
  if curl -fsSL "$url" -o "$tmp" 2>/dev/null && [ -s "$tmp" ]; then
    install -m 0755 "$tmp" "$BIN_DIR/$BIN"
    rm -f "$tmp"
    log "Установлен готовый бинарь -> $BIN_DIR/$BIN"
    return
  fi
  rm -f "$tmp"

  log "Готового бинаря нет — собираю из исходников (нужен Go)."
  ensure_go
  local src
  src="$(mktemp -d)"
  log "Клонирую $REPO …"
  git clone --depth 1 "$REPO" "$src" >/dev/null 2>&1 || die "git clone не удался"
  ( cd "$src" && CGO_ENABLED=0 go build -o "$BIN_DIR/$BIN" ./cmd/deploystack ) \
    || die "сборка Go не удалась"
  rm -rf "$src"
  log "Собран бинарь -> $BIN_DIR/$BIN"
}

ensure_go() {
  if command -v go >/dev/null 2>&1; then return; fi
  log "Go не найден — устанавливаю…"
  if   command -v apt-get >/dev/null 2>&1; then apt-get update -qq && apt-get install -y -qq golang-go git curl
  elif command -v dnf     >/dev/null 2>&1; then dnf install -y -q golang git curl
  elif command -v yum     >/dev/null 2>&1; then yum install -y -q golang git curl
  else die "не смог поставить Go автоматически — установите вручную и повторите"; fi
}

main() {
  need_root_hint
  command -v curl >/dev/null 2>&1 || die "нужен curl"

  fetch_or_build

  log "Запускаю установку стека…"
  # Пробрасываем все флаги пользователя (--target, --bot-token и т.п.) в бинарь.
  exec "$BIN_DIR/$BIN" install "$@"
}

main "$@"
