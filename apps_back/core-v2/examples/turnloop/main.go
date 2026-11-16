// Источник: официальный пример CloudWeGo Eino, раздел Part 4: Complete Example.
// https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_cancel_and_turnloop_quickstart/#part-4-complete-example
// Исполняемый код перенесён без изменений; комментарии переведены и дополнены.

package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// Хранилище контрольных точек из официального примера.
// Здесь сохраняется состояние выполнения TurnLoop, а не история сообщений чата.

type InMemoryStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{m: make(map[string][]byte)}
}

func (s *InMemoryStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.m[id]
	return data, ok, nil
}

func (s *InMemoryStore) Set(_ context.Context, id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = data
	return nil
}

// Delete позволяет Eino удалять сохранённое состояние после завершения работы.
func (s *InMemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

// Простейший агент из примера возвращает входной текст без обращения к модели.
// Параметры AgentRunOption он игнорирует: полноценная отмена через WithCancel здесь не реализована.

type echoAgent struct{}

func (a *echoAgent) Name(_ context.Context) string        { return "EchoAgent" }
func (a *echoAgent) Description(_ context.Context) string { return "echoes input" }

func (a *echoAgent) Run(ctx context.Context, input *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer gen.Close()
		// Задержка имитирует выполнение задачи. Отмена переданного контекста прерывает ожидание.
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			gen.Send(&adk.AgentEvent{Err: ctx.Err()})
			return
		}
		// Результат передаётся через итератор событий; defer выше закрывает его после завершения.
		reply := "Echo: "
		if len(input.Messages) > 0 {
			reply += input.Messages[0].Content
		}
		gen.Send(&adk.AgentEvent{
			AgentName: "EchoAgent",
			Output: &adk.AgentOutput{
				MessageOutput: &adk.MessageVariant{
					Message: schema.AssistantMessage(reply, nil),
				},
			},
		})
	}()
	return iter
}

// Правило приоритетов принадлежит приложению: сам TurnLoop его не задаёт.

func sortByPriority(items []string) []string {
	sorted := make([]string, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		// Сообщения с «!» идут первыми. Стабильная сортировка сохраняет порядок остальных.
		return strings.HasPrefix(sorted[i], "!") && !strings.HasPrefix(sorted[j], "!")
	})
	return sorted
}

// Создание, запуск, пополнение очереди и завершение TurnLoop.

func main() {
	ctx := context.Background()
	store := NewInMemoryStore()
	agent := &echoAgent{}

	cfg := adk.TurnLoopConfig[string, *schema.Message]{
		GenInput: func(ctx context.Context, loop *adk.TurnLoop[string, *schema.Message], items []string) (*adk.GenInputResult[string, *schema.Message], error) {
			// GenInput получает накопленные команды на границе витков.
			// Один элемент выполняется сейчас, Remaining возвращается в очередь следующих витков.
			// История разговора в этом учебном примере не передаётся.
			sorted := sortByPriority(items)
			return &adk.GenInputResult[string, *schema.Message]{
				Input:     &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage(sorted[0])}},
				Consumed:  sorted[:1],
				Remaining: sorted[1:],
			}, nil
		},

		GenResume: func(ctx context.Context, loop *adk.TurnLoop[string, *schema.Message], interruptedItems, unhandledItems, newItems []string) (*adk.GenResumeResult[string, *schema.Message], error) {
			all := append(append(interruptedItems, unhandledItems...), newItems...)
			return &adk.GenResumeResult[string, *schema.Message]{
				Consumed:  all[:1],
				Remaining: all[1:],
			}, nil
		},

		// Для каждого витка возвращается один и тот же экземпляр агента.
		PrepareAgent: func(ctx context.Context, loop *adk.TurnLoop[string, *schema.Message], consumed []string) (adk.Agent, error) {
			return agent, nil
		},

		OnAgentEvents: func(ctx context.Context, tc *adk.TurnContext[string, *schema.Message], events *adk.AsyncIterator[*adk.AgentEvent]) error {
			for {
				event, ok := events.Next()
				if !ok {
					break
				}
				// Эти каналы сообщают о принятой отмене текущего витка.
				// Они не служат проверкой запуска или завершения всего TurnLoop.
				select {
				case <-tc.Preempted:
					log.Println("Preempted by higher priority message")
				case <-tc.Stopped:
					log.Printf("Service shutting down: %s", tc.StopCause())
				default:
				}
				if event.Err != nil {
					// Поведение исходника сохранено: здесь возвращается любая ошибка события.
					// В документации отдельно указано не возвращать CancelError при вытеснении.
					return event.Err
				}
				if event.Output != nil && event.Output.MessageOutput != nil {
					fmt.Printf("[%s] %s\n", event.AgentName, event.Output.MessageOutput.Message.Content)
				}
			}
			return nil
		},

		Store:        store,
		CheckpointID: "session-123",
	}

	// NewTurnLoop создаёт объект без запуска горутины обработки.
	loop := adk.NewTurnLoop(cfg)
	loop.Push("normal message")
	loop.Push("low priority task")
	// Run запускает фоновую обработку и сразу возвращается. Push до запуска разрешён.
	loop.Run(ctx)

	// Пример запроса вытеснения: сообщение добавляется в очередь вместе с запросом отмены.
	// У echoAgent нет поддержки WithCancel, поэтому заглушка может завершить текущую работу сама.
	time.AfterFunc(200*time.Millisecond, func() {
		accepted, ack := loop.Push("!urgent message",
			adk.WithPreempt[string, *schema.Message](adk.AnySafePoint),
		)
		if accepted {
			<-ack
			log.Println("Preemption signal acknowledged")
		}
	})

	// Stop завершает весь цикл; это не кнопка отмены только одного ответа.
	// Для агента без поддержки WithCancel остановка происходит после текущего витка.
	time.AfterFunc(2*time.Second, func() {
		loop.Stop(
			adk.WithGraceful(),
			adk.WithStopCause("demo timeout"),
		)
	})

	// Wait вызывается после Run и блокирует main до полного завершения цикла.
	result := loop.Wait()
	fmt.Printf("Exit reason: %v\n", result.ExitReason)
	fmt.Printf("Stop cause: %s\n", result.StopCause)
	fmt.Printf("checkpoint: attempted=%v, err=%v\n", result.CheckpointAttempted, result.CheckpointErr)

	// Повторный запуск с той же конфигурацией восстановления в исходнике лишь описан.
	// Новый экземпляр TurnLoop с тем же CheckpointID может загрузить сохранённое состояние.
}
