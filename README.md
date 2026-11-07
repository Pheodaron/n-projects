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

Wails запускается отдельно на компьютере пользователя и обращается к backend через generated Go Connect-клиент. Параметры подключения задаются в окружении процесса согласно `.env.example`.

Нативные зависимости для сборки клиента: на Linux — `build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev`, на macOS — Xcode Command Line Tools, на Windows — WebView2. Локальные задачи запуска и сборки desktop удалены из mise; CI отдельно проверяет компиляцию клиента.

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
