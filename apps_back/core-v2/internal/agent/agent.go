package agent

import "context"

type Message struct {
	ID   uint64 `json:"id"`
	Text string `json:"text"`
}

// Agent принадлежит одной сессии. Движок вызывает Step последовательно.
type Agent interface {
	// Step выполняет один этап и возвращает true, если нужно продолжить без новых сообщений.
	// Новые сообщения поступают между этапами. Состояние агент хранит в своих полях.
	// При отмене ctx этап должен завершиться. Вызовы emit допустимы только до возврата из Step.
	// Отменённую работу агент сбрасывает сам; состояние разговора может сохранить.
	Step(ctx context.Context, messages []Message, emit func(string)) (bool, error)
}

// Echo нужен для запуска движка без подключения настоящего агента.
type Echo struct{}

func (*Echo) Step(ctx context.Context, messages []Message, emit func(string)) (bool, error) {
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		emit(message.Text)
	}
	return false, nil
}
