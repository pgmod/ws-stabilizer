# Многоэтапная сборка для максимально маленького образа

# Этап 1: Сборка приложения
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Копируем файлы зависимостей
COPY go.mod go.sum ./

# Загружаем зависимости
RUN go mod download

# Копируем исходный код
COPY *.go ./

# Собираем статически скомпилированный бинарник
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o ws-stabilizer .

# Этап 2: Финальный образ на базе distroless (минимальный размер)
FROM gcr.io/distroless/static-debian12:nonroot

# Копируем только бинарник
COPY --from=builder /app/ws-stabilizer /ws-stabilizer

# Открываем порт
EXPOSE 8080

# Точка входа
ENTRYPOINT ["/ws-stabilizer"]
CMD ["-listen", ":8080"]

