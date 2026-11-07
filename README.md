# n-projects — Core

Backend Go с ConnectRPC, Eino TurnLoop + ChatModelAgent и PostgreSQL. Backend и БД запускаются через Docker Compose одинаково для разработки и production. Wails — отдельный desktop-клиент.

## Подготовка

Нужны Docker с Compose и [mise](https://mise.jdx.dev). Для генерации кода и тестов инструменты закреплены в `.mise.toml`.

```sh
cp .env.example .env
mise trust
mise install
```

Заполните `.env` по комментариям в [`.env.example`](.env.example).

llama.cpp — уже работающий внешний сервис. Его адрес должен быть доступен из контейнера: `localhost` внутри контейнера указывает на сам контейнер. Веса модели и inference-сервер в образ не входят.

## Сборка образа

Из корня репозитория:

```sh
docker build -t n-projects:local .
```

Dockerfile имеет builder-стадию Go и runtime `gcr.io/distroless/static-debian12:nonroot`. В runtime копируется только статически собранный backend. CA-сертификаты и непривилегированный пользователь уже предоставлены distroless. Goose, миграции, frontend, Wails и Go toolchain в образ backend не входят.

## Запуск

Отдельно от сборки:

```sh
docker compose --env-file .env -f deploy/compose.yaml up -d --wait postgres
mise run migrate
mise run compose:up
```

Последняя команда эквивалентна:

```sh
docker compose --env-file .env -f deploy/compose.yaml up -d --wait
```

Порядок старта: PostgreSQL в Compose → явный запуск Goose из корня проекта → backend в Compose. Миграции выполняются до запуска/обновления backend; автоматического применения схемы при compose up нет. В Compose нет `build` и монтирования исходников. После изменения кода повторите отдельную команду сборки и затем команду запуска.

Для registry-образа настройте `.env` по шаблону, выполните `docker compose --env-file .env -f deploy/compose.yaml pull`, затем запустите Compose.

Порты backend и PostgreSQL по умолчанию опубликованы только на loopback хоста. История сохраняется в volume PostgreSQL. Остановка с сохранением данных:

```sh
docker compose --env-file .env -f deploy/compose.yaml down
```

`/healthz` проверяет backend, `/readyz` дополнительно проверяет доступность выбранной модели. Ошибка внешнего inference не отменяет сохранность чатов.

## Миграции

SQL находится в `migrations/`. Goose установлен через mise и запускается из корня проекта:

```sh
mise run migrate
mise run migrate:status
mise run migrate:down
```

Goose подключается к опубликованному порту PostgreSQL согласно `.env.example`. В Compose нет сервиса миграций; backend не изменяет схему БД. Откат первой миграции удаляет Core-таблицы. Первая миграция сохраняет данные установок, созданных до внедрения Goose.

## Desktop-клиент

Wails запускается отдельно на компьютере пользователя и обращается к backend через generated Go Connect-клиент. По умолчанию подключается к `http://127.0.0.1:8080` с токеном `dev-token`; настройка env для desktop не требуется.

```sh
./desktop
./desktop --backend-url https://agent.example.com
./desktop --backend-url https://agent.example.com --token custom-token
./desktop --help
```

Токен клиента должен совпадать с настроенным на backend. Серверные параметры остаются в `.env.example`.

### Сборка бинарников в контейнере

Нужен Docker с BuildKit. Go, Node и нативные build-зависимости устанавливаются внутри `Dockerfile.desktop`. Основной `Dockerfile` backend не изменяется. Сборка рассчитана на **amd64 (x86_64)**; на ARM-хосте требуется эмуляция linux/amd64, например встроенная в Docker Desktop.

Из корня проекта:

```sh
mise run build:desktop:linux
mise run build:desktop:windows
```

Эквивалентные Docker-команды:

```sh
docker build --platform linux/amd64 -f Dockerfile.desktop --target linux-export --output type=local,dest=dist/linux-amd64 .
docker build --platform linux/amd64 -f Dockerfile.desktop --target windows-export --output type=local,dest=dist/windows-amd64 .
```

Готовые файлы для распространения:

- `dist/linux-amd64/n-projects` — Linux amd64, собирается на Ubuntu 24.04 (glibc 2.39). На машине пользователя нужны GTK4 и WebKitGTK 6.0. Для Ubuntu 24.04: `sudo apt-get install libgtk-4-1 libwebkitgtk-6.0-4`. Совместимость с более старыми дистрибутивами не гарантируется.
- `dist/windows-amd64/n-projects.exe` — Windows amd64 с установленным Microsoft Edge WebView2 Runtime. Собирается с GUI subsystem, без отдельного окна консоли.

Frontend собирается в контейнере и встраивается в executable через `go:embed`. Папку frontend, Go, Node, контейнер и `.env` конечному пользователю передавать не нужно. Бинарник не включает backend, базу данных и модель; он подключается к работающему серверу через `--backend-url`. Параметр `--token` меняет токен при необходимости. Ключи и `.env` исключены из build context.

В GitHub Actions отдельная задача **Desktop binaries** собирает оба варианта тем же Dockerfile и сохраняет их как артефакты `desktop-linux-amd64` и `desktop-windows-amd64` на странице запуска CI. Бинарники можно скачать оттуда и передать пользователям. После распаковки Linux-артефакта из CI восстановите право запуска: `chmod +x n-projects`. Публикация GitHub Release и цифровая подпись не выполняются автоматически.

## Контракт и тесты

```sh
mise run generate
mise run fmt
mise run test
```

`generate` вызывает установленный через mise protoc и закреплённые Go/Connect plugins. Generated-код включён в Git. Тесты используют отдельные схемы PostgreSQL и проверяют streaming через Eino/Connect, историю, идемпотентность, отмену, восстановление run и Goose Up/Down. Проверка конкретной модели выполняется на вашем внешнем endpoint.

## Поведение Core

Чаты создаются, переименовываются и удаляются после завершения активного run. Enter отправляет сообщение, Shift+Enter добавляет строку. Stop отменяет генерацию; отключение desktop/stream не отменяет серверный run. Следующее подключение получает актуальный snapshot с revision.

Каждый run получает ограниченный TurnLoop с ChatModelAgent и историей из PostgreSQL. Один слот генерации, один активный run на чат. После рестарта незавершённые runs становятся `interrupted`, частичный текст сохраняется. Checkpoints, субагенты и OIDC относятся к следующим этапам.

Архитектура: [ARCHITECTURE.md](ARCHITECTURE.md). План: [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md).
