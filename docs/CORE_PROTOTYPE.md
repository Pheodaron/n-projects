# Core prototype

Рабочий каталог для дальнейшей разработки: `apps_back/core-prototype`.
Это отдельное приложение и Go-модуль, выделенные из предоставленного пользователем примера в ветке `core/architecture-review`.

```text
apps_back/core-prototype/
  cmd/server/main.go        HTTP-пример: /send, /stop, /events
  pkg/agentcore/
    agent.go                фабрика llama.cpp / Qwen
    engine.go               менеджер сессий
    ports.go                HistoryStore и MemoryStore
    session.go              TurnLoop и подписки
    types.go                команды и события
  go.mod
  go.sum
  mise.toml
  Dockerfile
```

При переносе изменён только import собственного пакета в `main.go`. Расположение `pkg/agentcore` сохранено из примера для дальнейшего обсуждения границ пакета. Исходники существующих server и desktop не изменены; локальные `replace` и корневой Go workspace не добавлены.

Для воспроизводимости закреплены Go 1.27.3, Eino v0.9.23 и agenticopenai v0.2.4; `go.sum` создан через `go mod tidy`.

Пример сохранён как исходная точка, включая уже найденные ошибки. В частности, в `session.go` поле `loop` объявлено как `atomic.Pointer`, но используется как обычный указатель на TurnLoop. До исправления этой и остальных проблем сборка не ожидается успешной. Анализ и предлагаемые исправления: [CORE_REVIEW.md](CORE_REVIEW.md).

Проверка переноса: все шесть Go-файлов сопоставлены с исходным вложением, зависимости разрешены. Запущенный через mise `go build ./...` подтвердил ошибки типа `loop` в `session.go`: несовместимое присваивание и отсутствующие методы `Run/Push/Stop/Wait`. Эти ошибки намеренно оставлены для следующего шага работы с прототипом. Docker image не собирался.

Инструменты и команды задаются локальным `mise.toml`:

```sh
cd apps_back/core-prototype
mise trust
mise install
mise run deps
mise run fmt
mise run test
mise run vet
mise run build
```

`build` собирает отдельный Docker image. В общий Compose приложение будет подключено после исправления исходника и настройки запуска; текущие backend-сервисы продолжают использовать прежний Compose. Новых переменных окружения в этом переносе нет: параметры модели остались в исходном `main.go`.
