# n-projects — Core

Go backend + Wails desktop + ConnectRPC + Eino `TurnLoop` / `ChatModelAgent` + llama.cpp. Чаты и сообщения сохраняются в PostgreSQL. Это первый этап: текстовый диалог, история, потоковый ответ, Stop и восстановление UI после переподключения.

## Подготовка

Установите [mise](https://mise.jdx.dev), Docker с Compose и нативные зависимости Wails. Для Linux (Debian/Ubuntu): `build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev`. macOS требует Xcode Command Line Tools; Windows — WebView2. Python 3 используется для локальных launchers.

```sh
mise trust
mise install
cp .env.example .env
```

Задайте собственный `DEV_TOKEN`. Desktop хранит его в Go-слое и передаёт в Connect metadata. Core использует одного dev-пользователя и один workspace. Удалённый сервер запускайте за TLS proxy; локальная конфигурация слушает loopback.

## Подключение к готовому llama.cpp

Основной сценарий — уже работающий внешний llama-server. Задайте в `.env`:

```dotenv
LLAMA_BASE_URL=http://your-inference-host:8081/v1
LLAMA_MODEL=your-model-id
LLAMA_API_KEY=
```

`LLAMA_MODEL` должен совпадать с ID из `/v1/models`. `LLAMA_API_KEY` нужен только если сервис требует авторизацию. Установка llama.cpp и загрузка весов не входят в обязательный запуск. Backend обращается к модели через OpenAI-compatible API; desktop обращается только к backend через generated Connect-клиент.

Опционально можно использовать собственный локальный `llama-server`: задайте `LLAMA_SERVER` и `LLAMA_MODEL_FILE`, затем `mise run dev:llama`. Если путь к модели не задан, `mise run dev` подключается к внешнему endpoint.

## Запуск

```sh
mise run generate
mise run dev
```

`dev` поднимает PostgreSQL, локальный llama-server при указанном файле, backend и desktop, ждёт readiness и завершает созданные процессы при выходе. Docker volume с историей остаётся. Компоненты можно запускать отдельно:

```sh
mise run infra
mise run migrate
mise run dev:server
mise run dev:desktop
```

Для подключения desktop к отдельному серверу задайте `BACKEND_URL` и тот же dev token. `/healthz` проверяет backend, `/readyz` дополнительно проверяет доступность выбранной модели.

## Использование

Создайте чат, отправьте сообщение и дождитесь потокового ответа. Enter отправляет, Shift+Enter добавляет строку. Stop отменяет генерацию; закрытие окна/подписки её не отменяет. Чаты можно переименовывать и удалять после завершения активного run.

Каждый принятый run выполняется через настоящий Eino TurnLoop с ChatModelAgent. В Core loop ограничен одним ходом, после чего завершается. Следующий запрос получает историю из БД. Один слот генерации и один активный run на чат; очередь, checkpoints и resume относятся к следующему этапу. При перезапуске backend незавершённые runs отмечаются `interrupted`, сохраняют доступный текст и освобождают чат. Второй backend на той же БД отклоняется advisory lock.

SSE используется внутри адаптера llama.cpp. Собственный API — ConnectRPC: unary-команды и server-streaming `WatchRun`. Поток передаёт полные snapshots с revision; reconnect повторно получает актуальный snapshot. Изменяющие запросы имеют `request_id` для защиты от сетевых дублей.

Core хранит сессии/историю и run snapshots в PostgreSQL JSONB, а результаты идемпотентных команд — в receipts. Это сохраняет согласованность одним коротким SQL transaction и не требует отдельной event bus. История ограничивается последними завершёнными ходами; UI показывает, если старая часть не вошла в контекст.

## Проверки и сборка

```sh
mise run proto:check
mise run generate
mise run test
mise run build
```

Интеграционная проверка PostgreSQL запускается явно:

```sh
TEST_DATABASE_URL=postgres://nprojects:dev@127.0.0.1:5432/nprojects?sslmode=disable mise run test
```

Тест создаёт отдельную схему и удаляет её после завершения; использует настоящий PostgreSQL, Connect-клиент и Eino TurnLoop с управляемым HTTP model fixture. Он проверяет streaming, историю, владение, идемпотентность и CRUD. Такая проверка не подтверждает совместимость произвольного GGUF. Приёмка конкретной модели требует реального llama-server: первый ответ, второй ход с контекстом, Stop и успешный следующий ответ.

Go bindings используют закреплённый Wails fork commit `1d1c0c36d0bc`; frontend runtime соответствует его версии `3.1.0-nightly.20261028`. Сгенерированные Protobuf/Connect-файлы и pnpm lockfile включены в Git.

Архитектура: [ARCHITECTURE.md](ARCHITECTURE.md). Порядок последующих работ: [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md), этапы 1 → 4 → 5 → 3 → 2 и далее.
