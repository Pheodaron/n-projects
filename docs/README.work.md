# n-projects

Monorepo: Wails3 desktop-клиент, Go backend с ConnectRPC и Eino TurnLoop + ChatModelAgent, PostgreSQL. llama.cpp — внешний сервис.

```text
apps/desktop/         Wails3-приложение: main.go, internal/, frontend/, mise.toml, Dockerfile
apps_back/server/     Backend: cmd/server/, internal/, mise.toml, Dockerfile
proto/                Общий protobuf-контракт
migrations/           SQL-миграции Goose
compose.yaml          Общий запуск backend-сервисов и PostgreSQL
mise.toml             Общие инструменты и миграции
docs/                 Архитектура и план реализации
```

Desktop и server — самостоятельные Go-проекты со своими `go.mod/go.sum`, зависимостями и Docker build context. Общего Go-модуля и workspace нет. Каждый проект хранит собственный generated-код в `internal/gen`; источник контракта — общий `proto/`. Для сборки достаточно каталога приложения; для повторной генерации кода нужен общий `.proto`, для DB-тестов сервера — корневая `migrations/`.

## Backend

Нужны Docker с Compose и [mise](https://mise.jdx.dev). Из корня репозитория:

```sh
cp .env.example .env
mise trust
mise install
```

Заполните `.env` по комментариям в [`.env.example`](.env.example). Адрес готового llama.cpp должен быть доступен из контейнера; модель в образ не входит.

Каждый backend-сервис собирается через собственные `mise.toml` и `Dockerfile`. Для существующего сервера:

```sh
cd apps_back/server
mise trust
mise install
mise run build
cd ../..
```

Сборка использует каталог сервиса как Docker context. Эквивалентная команда из корня:

```sh
docker build -t n-projects:local apps_back/server
```

Runtime — `gcr.io/distroless/static-debian12:nonroot`, в образ копируется только backend-бинарник. Frontend, Wails, Goose и Go toolchain в runtime не входят.

Все серверные сервисы запускаются через **общий `compose.yaml`**. В Compose нет `build` и сервиса миграций:

```sh
docker compose --env-file .env -f compose.yaml up -d --wait postgres
mise run migrate
mise run compose:up
```

Миграции выполняются с хоста из корня до запуска или обновления backend. После изменения кода отдельно пересоберите образ и повторите запуск Compose. PostgreSQL сохраняет данные в volume; порты по умолчанию опубликованы на loopback.

```sh
mise run migrate:status
mise run migrate:down
docker compose --env-file .env -f compose.yaml down
```

Откат первой миграции удаляет Core-таблицы. `/healthz` проверяет backend; `/readyz` также проверяет выбранную модель.

## Desktop

Собственная настройка — [`apps/desktop/mise.toml`](apps/desktop/mise.toml): Go `latest`, Node `latest`, pnpm и Wails3 CLI `v3.0.0`. `mise install` устанавливает CLI через `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0` с указанным GOPROXY; ручной установки CLI не требуется.

```sh
cd apps/desktop
mise trust
mise install
mise run doctor
mise run dev
mise run production
```

`doctor` вызывает `wails3 doctor`, `dev` — `wails3 dev`, `production` — `wails3 build`. Команды `wails3 production` у CLI нет. Минимальные `Taskfile.yml` и `build/config.yml` задают сборку frontend, бинарника и dev watcher. Production-бинарник записывается в `apps/desktop/bin/n-projects` (`n-projects.exe` на Windows).

Для нативной сборки Linux нужны C compiler, pkg-config, GTK4 и WebKitGTK 6.0; Ubuntu 24.04: `sudo apt-get install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev`. `mise install` устанавливает инструменты, системные библиотеки проверяются через `doctor`.

### Сборка Linux и Windows в контейнере

Из `apps/desktop`, нужен Docker с BuildKit:

```sh
mise run build:linux
mise run build:windows
```

Из корня те же сборки доступны напрямую:

```sh
docker build --platform linux/amd64 --target linux-export --output type=local,dest=dist/linux-amd64 apps/desktop
docker build --platform linux/amd64 --target windows-export --output type=local,dest=dist/windows-amd64 apps/desktop
```

- `dist/linux-amd64/n-projects` — динамическая Linux amd64 сборка на Ubuntu 24.04, glibc 2.39. Для запуска нужны GTK4 и WebKitGTK 6.0; Ubuntu 24.04: `sudo apt-get install libgtk-4-1 libwebkitgtk-6.0-4`.
- `dist/windows-amd64/n-projects.exe` — Windows amd64, нужен Microsoft Edge WebView2 Runtime. Отдельное окно консоли не открывается.

На ARM-хосте нужна эмуляция linux/amd64. Frontend встроен в бинарник; передавать пользователю исходники, Go, Node, Docker и `.env` не требуется. Клиент подключается к отдельно запущенному backend:

```sh
./n-projects
./n-projects --backend-url https://agent.example.com
./n-projects --backend-url https://agent.example.com --token custom-token
./n-projects --help
```

По умолчанию URL — `http://127.0.0.1:8080`, токен — `dev-token`. Токен должен совпадать с backend.

CI собирает оба бинарника в Docker и сохраняет артефакты `desktop-linux-amd64` и `desktop-windows-amd64`. После скачивания Linux-артефакта: `chmod +x n-projects`.

## Проверки

В каталоге нужного приложения (`apps/desktop` или `apps_back/server`):

```sh
mise run generate
mise run fmt
mise run test
mise run vet
```

`generate` обновляет только generated-код выбранного приложения из общего `.proto`, через `protoc`, без Buf. Protoc, Go/Connect plugins и задача генерации определены в `mise.toml` каждого приложения. CI запускает эти задачи отдельно и проверяет generated-код на соответствие исходному контракту.

Серверные тесты проверяют RPC/streaming, историю, идемпотентность, отмену, восстановление runs и Goose Up/Down. SQL берётся из корневой `migrations/`, Go-хелперы тестов находятся в `apps_back/server/internal/testdb`. DB-тестам нужен PostgreSQL. Desktop-задачи `test` и `vet` проверяют пакеты `internal/`; полная сборка Wails проверяется отдельно.

В Core один активный run на чат и один слот генерации. Stop отменяет run; отключение клиента не отменяет его. После рестарта незавершённые runs получают статус `interrupted`, частичный ответ сохраняется. Checkpoints, субагенты и OIDC относятся к следующим этапам.

Архитектура: [ARCHITECTURE.md](docs/ARCHITECTURE.md). План: [IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md).
