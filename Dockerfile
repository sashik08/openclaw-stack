# Dockerfile монитора (deploystack dashboard).
#
# Монитору нужен доступ к docker CLI хоста, поэтому:
#   - базовый образ docker:cli (содержит клиент `docker`);
#   - при запуске монтируется сокет демона и каталог установки с config.json.
#
# Сборка:
#   docker build -t openclaw-stack-monitor .
#
# Запуск (монитор + дашборд на :8088):
#   docker run -d --name openclaw-stack-monitor --restart always \
#     -p 127.0.0.1:8088:8088 \
#     -v /var/run/docker.sock:/var/run/docker.sock \
#     -v $HOME/.openclaw-stack:/data \
#     -e OPENCLAW_STACK_DIR=/data \
#     openclaw-stack-monitor
#
# Примечание: для доступа к другим контейнерам по 127.0.0.1 (health-пробы
# OpenClaw/OmniRoute) запускайте с `--network host` вместо `-p`, либо задайте
# health-URL на имена сервисов compose-сети.

# --- сборка статического бинаря ---
FROM golang:1.22-alpine AS build
WORKDIR /src
# Сначала модуль — кэшируется, если зависимости не менялись (тут только stdlib).
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/deploystack ./cmd/deploystack

# --- финальный образ с docker-клиентом ---
FROM docker:cli
COPY --from=build /out/deploystack /usr/local/bin/deploystack
ENV OPENCLAW_STACK_DIR=/data
VOLUME ["/data"]
EXPOSE 8088
ENTRYPOINT ["deploystack"]
CMD ["dashboard"]
