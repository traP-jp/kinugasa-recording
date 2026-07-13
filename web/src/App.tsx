import { FormEvent, useState } from "react";

import { ApiError, createSession } from "./api";

const namePattern = /^[A-Za-z0-9-]+$/;

function validateName(name: string): string | null {
  if (name.length === 0) {
    return "Session名を入力してください。";
  }
  if (new TextEncoder().encode(name).length > 255 || !namePattern.test(name)) {
    return "Session名は255 byte以内の英数字とハイフンで入力してください。";
  }

  return null;
}

export function App() {
  const [name, setName] = useState("");
  const [createdSession, setCreatedSession] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const validationError = validateName(name);
    if (validationError !== null) {
      setError(validationError);
      return;
    }

    setSubmitting(true);
    setError(null);
    try {
      const session = await createSession(name);
      setCreatedSession(session.name);
      window.history.pushState(
        {},
        "",
        `/sessions/${encodeURIComponent(session.name)}`,
      );
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "NAME_RESERVED") {
        setError("このSession名は現在または過去に使用されています。");
      } else {
        setError(
          "Sessionを作成できませんでした。しばらく待ってから再試行してください。",
        );
      }
    } finally {
      setSubmitting(false);
    }
  }

  if (createdSession !== null) {
    return (
      <main>
        <p className="eyebrow">Session</p>
        <h1>{createdSession}</h1>
        <p>Sessionを作成しました。</p>
      </main>
    );
  }

  return (
    <main>
      <h1>Kinugasa Recording</h1>
      <p>新しい録画Sessionを作成します。</p>
      <form onSubmit={submit} noValidate>
        <label htmlFor="session-name">Session名</label>
        <input
          id="session-name"
          name="sessionName"
          value={name}
          onChange={(event) => setName(event.target.value)}
          autoComplete="off"
          aria-describedby={error === null ? undefined : "session-error"}
        />
        <button type="submit" disabled={submitting}>
          {submitting ? "作成中…" : "Sessionを作成"}
        </button>
      </form>
      {error !== null && (
        <p id="session-error" role="alert">
          {error}
        </p>
      )}
    </main>
  );
}
