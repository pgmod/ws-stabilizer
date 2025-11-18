# WebSocket Stabilizer

Прокси-сервер для стабилизации WebSocket соединений с автоматическим переподключением к бэкенду.

## Использование с Docker

### Быстрый старт

```bash
# Сборка и запуск
docker-compose up -d

# Просмотр логов
docker-compose logs -f

# Остановка
docker-compose down
```

### Настройка через docker-compose.yml

Отредактируйте `docker-compose.yml` и измените параметры в секции `command`:

```yaml
command:
  - "-listen"
  - ":8080"              # Порт для прослушивания
  - "-backend"
  - "ws://your-backend:80/api/ws"  # URL бэкенда
  - "-dial-timeout"
  - "5s"                 # Таймаут подключения
  - "-retry-backoff"
  - "200ms"              # Задержка между попытками переподключения
```

### Запуск с кастомными параметрами

```bash
docker run -d \
  --name ws-stabilizer \
  -p 8080:8080 \
  ws-stabilizer:latest \
  -listen :8080 \
  -backend ws://your-backend:80/api/ws \
  -dial-timeout 5s \
  -retry-backoff 200ms
```

### Параметры командной строки

- `-listen`, `-l` - адрес для прослушивания (обязательно)
- `-backend`, `-b` - URL бэкенд WebSocket сервера (по умолчанию: `ws://localhost:80/api/ws`)
- `-dial-timeout`, `-t` - таймаут подключения к бэкенду (по умолчанию: `5s`)
- `-retry-backoff`, `-r` - задержка между попытками переподключения (по умолчанию: `200ms`)
- `-disconnected-event`, `-de` - имя события при отключении (по умолчанию: `backend_disconnected`)
- `-connected-event`, `-ce` - имя события при подключении (по умолчанию: `backend_connected`)
- `-help`, `-h` - показать справку

## Локальная сборка

```bash
# Сборка образа
docker build -t ws-stabilizer:latest .

# Запуск
docker run -p 8080:8080 ws-stabilizer:latest -listen :8080 -backend ws://your-backend:80/api/ws
```

## Пример использования

1. Запустите стабилизатор:
   ```bash
   docker-compose up -d
   ```

2. Подключитесь к стабилизатору вместо прямого подключения к бэкенду:
   ```javascript
   const ws = new WebSocket('ws://localhost:8080');
   ```

3. Стабилизатор автоматически переподключится к бэкенду при обрыве соединения и уведомит клиента событиями `backend_connected` и `backend_disconnected`.

