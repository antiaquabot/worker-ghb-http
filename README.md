# worker-ghb-http

Воркер для автоматического уведомления об открытии регистрации на объекты застройщика **GHB**. Работает через прямые
HTTP-запросы к API GHB и публичному SSE-стриму сервиса мониторинга [stroi.homes](https://stroi.homes).

**Застройщик:** GHB (зашит в бинарь, не читается из конфига)
**Лицензия:** MIT

---

## Установка

### Linux / macOS

```bash
# Скачать бинарь (заменить linux-amd64 на нужную платформу)
curl -L https://github.com/stroi-homes/worker-ghb-http/releases/latest/download/worker-ghb-http-linux-amd64 \
  -o worker-ghb-http
chmod +x worker-ghb-http
```

### Windows

Скачать `worker-ghb-http-windows-amd64.exe` со страницы [Releases](../../releases).

### Поддерживаемые платформы

| Файл                                | ОС      | Архитектура   |
|-------------------------------------|---------|---------------|
| `worker-ghb-http-linux-amd64`       | Linux   | x86_64        |
| `worker-ghb-http-linux-arm64`       | Linux   | ARM64         |
| `worker-ghb-http-darwin-amd64`      | macOS   | Intel         |
| `worker-ghb-http-darwin-arm64`      | macOS   | Apple Silicon |
| `worker-ghb-http-windows-amd64.exe` | Windows | x86_64        |

---

## Настройка

### 1. Создать конфиг

```bash
# Интерактивное создание зашифрованного конфига
./worker-ghb-http init --config config.yaml
```

Или скопировать пример вручную:

```bash
cp config.example.yaml config.yaml
# Отредактировать config.yaml
```

### 2. Заполнить конфиг

Обязательные поля:

- `telegram.bot_token` — токен вашего Telegram-бота (создать через [@BotFather](https://t.me/BotFather))
- `telegram.chat_id` — ваш Telegram chat_id (узнать через [@userinfobot](https://t.me/userinfobot))
- `watch_list` — список объектов для отслеживания

Для авторегистрации (`auto_register: true`) также нужно заполнить `personal_data`.

---

## Запуск

```bash
./worker-ghb-http --config config.yaml
```

При запуске запрашивается пароль для расшифровки `personal_data`. Для автозапуска используйте `WORKER_PASSWORD`:

```bash
WORKER_PASSWORD=мой_пароль ./worker-ghb-http --config config.yaml
```

---

## Режимы работы

| Настройка                                      | Поведение                     |
|------------------------------------------------|-------------------------------|
| `notify_on_open: true`, `auto_register: false` | Только уведомление в Telegram |
| `notify_on_open: true`, `auto_register: true`  | Уведомление + авторегистрация |

---

## Автозапуск

### systemd (Linux)

```ini
[Unit]
Description = worker-ghb-http
After = network.target

[Service]
ExecStart = /opt/worker-ghb-http/worker-ghb-http --config /opt/worker-ghb-http/config.yaml
Environment = WORKER_PASSWORD=мой_пароль
Restart = always
RestartSec = 5

[Install]
WantedBy = multi-user.target
```

### launchd (macOS)

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "...">
<plist version="1.0">
    <dict>
        <key>Label</key>
        <string>homes.stroi.worker-ghb-http</string>
        <key>ProgramArguments</key>
        <array>
            <string>/usr/local/bin/worker-ghb-http</string>
            <string>--config</string>
            <string>/usr/local/etc/worker-ghb-http/config.yaml</string>
        </array>
        <key>EnvironmentVariables</key>
        <dict>
            <key>WORKER_PASSWORD</key>
            <string>мой_пароль</string>
        </dict>
        <key>RunAtLoad</key>
        <true/>
        <key>KeepAlive</key>
        <true/>
    </dict>
</plist>
```

---

## Сборка из исходников

```bash
go build -o worker-ghb-http .

# Все платформы
make dist
```

Требования: Go 1.22+
