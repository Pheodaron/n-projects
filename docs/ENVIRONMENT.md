# Переменные окружения

Настройки задаются в `.env` в корне проекта. Шаблон — [`.env.example`](../.env.example). Mise читает файл автоматически; Compose получает его через `--env-file .env`. Готовому desktop-клиенту параметры передаются в окружении процесса.

- `APP_IMAGE` — готовый Docker-образ backend. По умолчанию `n-projects:local`; можно указать образ из registry, например `registry.example/n-projects:1.0`.
- `POSTGRES_PASSWORD` — пароль пользователя PostgreSQL, обязательная непустая строка. `dev` в шаблоне — пример. Пароль должен совпадать с указанным в обеих строках подключения.
- `POSTGRES_PORT` — опубликованный порт PostgreSQL на хосте. Целое число `1–65535`, по умолчанию `5432`. Публикуется только на `127.0.0.1`.
- `DATABASE_URL` — подключение Goose с хоста, обязательно для миграций. Пример: `postgres://nprojects:dev@127.0.0.1:5432/nprojects?sslmode=disable`.
- `BACKEND_DATABASE_URL` — подключение backend к PostgreSQL из контейнера, обязательно. Пример: `postgres://nprojects:dev@postgres:5432/nprojects?sslmode=disable`. Compose передаёт его серверу под именем `DATABASE_URL`.
- `DEV_TOKEN` — общий bearer token для backend и Wails-клиента. Обязательная непустая строка; значение на сервере и клиенте должно совпадать.
- `BACKEND_BIND` — IP хоста для публикации backend. По умолчанию `127.0.0.1` — доступ только с хоста; `0.0.0.0` — на всех IPv4-интерфейсах. Можно указать конкретный IP хоста.
- `BACKEND_PORT` — опубликованный порт backend на хосте. Целое число `1–65535`, по умолчанию `8080`.
- `BACKEND_URL` — адрес backend с компьютера Wails-клиента. HTTP(S) URL, по умолчанию `http://127.0.0.1:8080`; пример удалённого адреса — `https://agent.example.com`.
- `LLAMA_BASE_URL` — адрес OpenAI-compatible API llama.cpp, доступный из контейнера backend. Обязательный HTTP(S) URL с `/v1`, например `http://inference.example:8081/v1`. Без credentials, query и fragment. `localhost` здесь указывает на контейнер backend.
- `LLAMA_MODEL` — точный model ID из `/v1/models`, обязательная непустая строка. Возможные значения определяются вашим inference-сервисом; `local` — пример ID, если он настроен в llama.cpp.
- `LLAMA_API_KEY` — bearer token inference-сервиса. Необязательная строка; пустое значение означает запросы без заголовка авторизации.
- `TEST_DATABASE_URL` — подключение PostgreSQL с машины запуска тестов. Пример: `postgres://nprojects:dev@127.0.0.1:5432/nprojects?sslmode=disable`. Если не задана, DB-интеграционные тесты пропускаются. Пользователю БД нужны права создания и удаления тестовых схем.

Следующие параметры задаются самим Compose или mise; добавлять их в `.env` не нужно:

- `SERVER_ADDR` — адрес прослушивания внутри backend-контейнера, фиксирован в Compose: `0.0.0.0:8080`. Публикация на хосте настраивается через `BACKEND_BIND` и `BACKEND_PORT`.
- `POSTGRES_USER` — пользователь PostgreSQL, фиксирован в Compose: `nprojects`.
- `POSTGRES_DB` — имя базы PostgreSQL, фиксировано в Compose: `nprojects`.
- `GOOSE_DRIVER` — драйвер Goose, mise задаёт `postgres`.
- `GOOSE_DBSTRING` — строка подключения Goose, mise берёт значение из `DATABASE_URL`.

`DATABASE_URL` и `BACKEND_DATABASE_URL` указывают на одну БД через разные адреса. При изменении POSTGRES_PORT обновите DATABASE_URL и TEST_DATABASE_URL; при изменении BACKEND_PORT — BACKEND_URL клиента. Специальные символы пароля в URL нужно кодировать. Значения `sslmode` в PostgreSQL URL: `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full`; в примере локального Compose используется `disable`.

POSTGRES_PASSWORD задаёт пароль при инициализации пустого volume. Для существующей БД изменение `.env` само по себе пароль не меняет. Новые настройки контейнера применяются после его пересоздания через Compose. Рабочие пароли и токены в Git не добавляются.

При изменении env-контракта этот список и `.env.example` обновляются в том же коммите вместе со связанными настройками Compose/mise/CI.
