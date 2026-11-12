# n-projects

Monorepo: Wails3 desktop-клиент, Go backend с ConnectRPC и Eino TurnLoop + ChatModelAgent, PostgreSQL. llama.cpp — внешний сервис.

```text
apps/desktop/         Wails3-приложение: main.go, internal/, frontend/, mise.toml, Dockerfile
apps_back/server/     Backend: cmd/server/, internal/, mise.toml, Dockerfile
proto/                Общий protobuf-контракт
gen/go/               Generated Protobuf и ConnectRPC
migrations/           SQL-миграции Goose и проверки
compose.yaml          Общий запуск backend-сервисов и PostgreSQL
mise.toml             Общие инструменты, codegen, миграции и тесты
go.work               Связь трёх Go-модулей без публикации пакетов
```

Корневой Go-модуль содержит общий RPC-контракт и миграции. Desktop и server имеют собственные `go.mod/go.sum`, закрытые `internal/` и зависимости.

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

Сборка использует корень репозитория как Docker context. Эквивалентная команда из корня:

```sh
docker build -f apps_back/server/Dockerfile -t n-projects:local .
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
docker build --platform linux/amd64 -f apps/desktop/Dockerfile --target linux-export --output type=local,dest=dist/linux-amd64 .
docker build --platform linux/amd64 -f apps/desktop/Dockerfile --target windows-export --output type=local,dest=dist/windows-amd64 .
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

Из корня:

```sh
mise run generate
mise run fmt
mise run test
```

Codegen использует `protoc`, без Buf. Тесты проверяют RPC/streaming, историю, идемпотентность, отмену, восстановление runs и Goose Up/Down. DB-тестам нужен PostgreSQL. Проверки только сервера: `mise run test` и `mise run vet` из `apps_back/server`.

В Core один активный run на чат и один слот генерации. Stop отменяет run; отключение клиента не отменяет его. После рестарта незавершённые runs получают статус `interrupted`, частичный ответ сохраняется. Checkpoints, субагенты и OIDC относятся к следующим этапам.

Архитектура: [ARCHITECTURE.md](ARCHITECTURE.md). План: [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md).
