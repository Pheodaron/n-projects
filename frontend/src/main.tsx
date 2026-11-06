import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Call, Events } from "@wailsio/runtime";
import "./style.css";
type Session = { id: string; title: string; activeRunId?: string };
type Message = { id: string; role: string; content: string; status?: string };
type Run = {
  id: string;
  sessionId: string;
  status: string;
  message: Message;
  contextTruncated?: boolean;
  errorMessage?: string;
};
const call = async (method: string, data: unknown = {}) =>
  JSON.parse(
    (await Call.ByName(
      "github.com/Pheodaron/n-projects/internal/desktop.Bridge.Call",
      method,
      JSON.stringify(data),
    )) as string,
  );
const key = () => crypto.randomUUID();
function App() {
  const [sessions, setSessions] = useState<Session[]>([]),
    [selected, setSelected] = useState(""),
    [messages, setMessages] = useState<Message[]>([]),
    [run, setRun] = useState<Run>(),
    [text, setText] = useState(""),
    [error, setError] = useState(""),
    [pending, setPending] = useState(false);
  const submission = useRef<
    { sessionId: string; content: string; requestId: string } | undefined
  >(undefined);
  const active = useRef("");
  const end = useRef<HTMLDivElement>(null);
  const refresh = async () =>
    setSessions((await call("ListSessions")).sessions || []);
  const watch = (id: string) =>
    Call.ByName(
      "github.com/Pheodaron/n-projects/internal/desktop.Bridge.Watch",
      id,
    );
  const open = async (id: string) => {
    active.current = id;
    setSelected(id);
    setRun(undefined);
    setMessages([]);
    const [data, session] = await Promise.all([
      call("ListMessages", { sessionId: id }),
      call("GetSession", { sessionId: id }),
    ]);
    if (active.current !== id) return;
    setMessages(data.messages || []);
    if (session.session.activeRunId) {
      const r = (await call("GetRun", { runId: session.session.activeRunId }))
        .run;
      if (active.current === id) {
        setRun(r);
        watch(r.id);
      }
    }
  };
  const guard = async (fn: () => Promise<unknown>) => {
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(String(e));
    }
  };
  useEffect(() => {
    guard(refresh);
    const off = Events.On("run", (e: any) => {
      const r = JSON.parse(e.data) as Run;
      if (r.sessionId === active.current) {
        setRun(r);
        if (!["running", "cancelling"].includes(r.status)) {
          call("ListMessages", { sessionId: r.sessionId })
            .then((d) => {
              if (active.current === r.sessionId) setMessages(d.messages || []);
            })
            .catch((e) => setError(String(e)));
        }
      }
      refresh().catch(() => {});
    });
    const conn = Events.On("connection", (e: any) => setError(String(e.data)));
    return () => {
      off();
      conn();
    };
  }, []);
  useEffect(() => {
    end.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, run?.message?.content]);
  const create = () =>
    guard(async () => {
      const s = (
        await call("CreateSession", { requestId: key(), title: "Новый чат" })
      ).session;
      await refresh();
      await open(s.id);
    });
  const busy =
    pending || (!!run && ["running", "cancelling"].includes(run.status));
  const send = () =>
    guard(async () => {
      if (!text.trim() || busy) return;
      setPending(true);
      const prior = submission.current;
      const requestId =
        prior?.sessionId === selected && prior.content === text
          ? prior.requestId
          : key();
      submission.current = { sessionId: selected, content: text, requestId };
      try {
        const r = (
          await call("StartRun", {
            sessionId: selected,
            requestId,
            content: text,
          })
        ).run;
        submission.current = undefined;
        setText("");
        setRun(r);
        setMessages(
          (await call("ListMessages", { sessionId: selected })).messages || [],
        );
        await watch(r.id);
      } finally {
        setPending(false);
      }
    });
  return (
    <div className="layout">
      <aside>
        <div className="brand">
          n<span>·</span>projects<small>РАБОЧЕЕ ПРОСТРАНСТВО</small>
        </div>
        <button className="new" onClick={create}>
          ＋ Новый чат
        </button>
        <div className="label">ДИАЛОГИ</div>
        <nav>
          {sessions.map((s) => (
            <button
              className={s.id === selected ? "selected" : ""}
              key={s.id}
              onClick={() => guard(() => open(s.id))}
            >
              {s.title}
              <small>
                {s.activeRunId ? "● Выполняется" : "Сохранённый диалог"}
              </small>
            </button>
          ))}
        </nav>
        <footer>
          Локальная модель · llama.cpp
          <br />
          История сохраняется на сервере
        </footer>
      </aside>
      <main>
        <header>
          <strong>
            {sessions.find((s) => s.id === selected)?.title || "Ваш помощник"}
          </strong>
          {selected && (
            <div>
              <button
                onClick={() => {
                  const title = prompt("Название чата");
                  if (title)
                    guard(async () => {
                      await call("UpdateSession", {
                        sessionId: selected,
                        requestId: key(),
                        title,
                      });
                      await refresh();
                    });
                }}
              >
                Переименовать
              </button>
              <button
                disabled={busy}
                onClick={() => {
                  if (confirm("Удалить чат и историю?"))
                    guard(async () => {
                      await call("DeleteSession", {
                        sessionId: selected,
                        requestId: key(),
                      });
                      active.current = "";
                      setSelected("");
                      setMessages([]);
                      setRun(undefined);
                      await refresh();
                    });
                }}
              >
                Удалить
              </button>
            </div>
          )}
        </header>
        <section className="conversation">
          {!selected ? (
            <div className="welcome">
              <div className="orb">n</div>
              <h1>С чего начнём?</h1>
              <p>
                Создайте чат, задайте вопрос
                <br />и продолжайте разговор с вашим агентом.
              </p>
              <button onClick={create}>Создать первый чат ↗</button>
            </div>
          ) : (
            <>
              {messages.map((m) => (
                <article key={m.id} className={m.role}>
                  <div className="speaker">
                    {m.role === "user" ? "Вы" : "Помощник"}
                  </div>
                  <p>{m.content}</p>
                  {m.status === "incomplete" && (
                    <small>Незавершённый ответ</small>
                  )}
                </article>
              ))}
              {run && !messages.some((m) => m.id === run.message?.id) && (
                <article className="assistant">
                  <div className="speaker">
                    Помощник {busy && <span className="pulse">●</span>}
                  </div>
                  <p>{run.message?.content || "Готовлю ответ…"}</p>
                </article>
              )}
              <div ref={end} />
            </>
          )}
        </section>
        <div className="bottom">
          {error && (
            <div role="alert" className="error">
              {error}
              <button onClick={() => guard(refresh)}>
                Повторить подключение
              </button>
            </div>
          )}
          {run?.contextTruncated && (
            <small>Старая часть истории не вошла в контекст модели.</small>
          )}
          {run?.errorMessage && <div className="error">{run.errorMessage}</div>}
          {selected && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                send();
              }}
            >
              <textarea
                aria-label="Сообщение"
                placeholder="Напишите сообщение…"
                value={text}
                disabled={pending}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    send();
                  }
                }}
              />
              {busy ? (
                <button
                  type="button"
                  disabled={!run}
                  onClick={() =>
                    guard(() =>
                      call("CancelRun", { runId: run?.id, requestId: key() }),
                    )
                  }
                >
                  ■ Стоп
                </button>
              ) : (
                <button disabled={!text.trim()} type="submit">
                  Отправить ↑
                </button>
              )}
            </form>
          )}
          <small className="hint">
            Enter — отправить · Shift + Enter — новая строка
          </small>
        </div>
      </main>
    </div>
  );
}
createRoot(document.getElementById("root")!).render(<App />);
