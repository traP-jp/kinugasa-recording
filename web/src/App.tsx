import { FormEvent, useEffect, useState } from "react";
import { QRCodeSVG } from "qrcode.react";

import {
  ApiError,
  CameraStatus,
  SessionResource,
  addCamera,
  createSession,
  deleteCamera,
  getSession,
} from "./api";
import { Preview } from "./Preview";

const namePattern = /^[A-Za-z0-9-]+$/;

function validateName(name: string, kind: "Session" | "Camera"): string | null {
  if (name.length === 0) return `${kind}名を入力してください。`;
  if (new TextEncoder().encode(name).length > 255 || !namePattern.test(name))
    return `${kind}名は255 byte以内の英数字とハイフンで入力してください。`;
  return null;
}

function initialSessionName() {
  const match = window.location.pathname.match(/^\/sessions\/([^/]+)$/);
  return match ? decodeURIComponent(match[1]) : null;
}

export function App() {
  const [sessionName, setSessionName] = useState<string | null>(
    initialSessionName,
  );
  const [session, setSession] = useState<SessionResource | null>(null);
  const [name, setName] = useState("");
  const [cameraName, setCameraName] = useState("");
  const [connectionURLs, setConnectionURLs] = useState<
    Record<string, { rist?: string; srt?: string }>
  >({});
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (sessionName === null) return;
    let cancelled = false;
    const refresh = () =>
      void getSession(sessionName)
        .then((updated) => {
          if (!cancelled) setSession(updated);
        })
        .catch(() => {
          if (!cancelled) setError("Session状態を取得できませんでした。");
        });
    if (session === null) refresh();
    const timer = window.setInterval(refresh, 2000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [sessionName]);

  async function submitSession(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const validationError = validateName(name, "Session");
    if (validationError !== null) {
      setError(validationError);
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const created = await createSession(name);
      setSession(created);
      setSessionName(created.name);
      window.history.pushState(
        {},
        "",
        `/sessions/${encodeURIComponent(created.name)}`,
      );
    } catch (caught) {
      setError(
        caught instanceof ApiError && caught.code === "NAME_RESERVED"
          ? "このSession名は現在または過去に使用されています。"
          : "Sessionを作成できませんでした。しばらく待ってから再試行してください。",
      );
    } finally {
      setSubmitting(false);
    }
  }

  async function submitCamera(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (sessionName === null || session === null) return;
    const validationError = validateName(cameraName, "Camera");
    if (validationError !== null) {
      setError(validationError);
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const added = await addCamera(sessionName, cameraName);
      if (added.connectionUrls)
        setConnectionURLs((current) => ({
          ...current,
          [cameraName]: added.connectionUrls ?? {},
        }));
      setSession({
        ...session,
        spec: {
          ...session.spec,
          cameras: [
            ...session.spec.cameras,
            { name: cameraName, desiredState: "Present" },
          ],
        },
        status: {
          cameras: [
            ...session.status.cameras,
            { name: cameraName, phase: "Provisioning" },
          ],
        },
      });
      setCameraName("");
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "NAME_RESERVED")
        setError("このCamera名は現在または過去に使用されています。");
      else if (caught instanceof ApiError && caught.code === "TAKE_RECORDING")
        setError("録画中はCameraを追加できません。");
      else setError("Cameraを追加できませんでした。");
    } finally {
      setSubmitting(false);
    }
  }

  async function removeCamera(camera: string) {
    if (sessionName === null || session === null) return;
    setSubmitting(true);
    setError(null);
    try {
      await deleteCamera(sessionName, camera);
      setSession({
        ...session,
        spec: {
          ...session.spec,
          cameras: session.spec.cameras.map((item) =>
            item.name === camera ? { ...item, desiredState: "Absent" } : item,
          ),
        },
        status: {
          cameras: session.status.cameras.map((item) =>
            item.name === camera ? { ...item, phase: "Deleting" } : item,
          ),
        },
      });
    } catch (caught) {
      setError(
        caught instanceof ApiError && caught.code === "TAKE_RECORDING"
          ? "録画中はCameraを削除できません。"
          : "Cameraを削除できませんでした。",
      );
    } finally {
      setSubmitting(false);
    }
  }

  if (sessionName !== null) {
    const recording =
      session?.spec.takes.some((take) => take.desiredState === "Recording") ??
      false;
    const cameras = (session?.status.cameras ?? []).filter(
      (camera) => camera.phase !== "Removed",
    );
    return (
      <main>
        <p className="eyebrow">Session</p>
        <h1>{sessionName}</h1>
        <section aria-labelledby="camera-heading">
          <h2 id="camera-heading">Cameras</h2>
          <form onSubmit={submitCamera} noValidate>
            <label htmlFor="camera-name">Camera名</label>
            <input
              id="camera-name"
              value={cameraName}
              onChange={(event) => setCameraName(event.target.value)}
              disabled={recording || submitting}
            />
            <button type="submit" disabled={recording || submitting}>
              Cameraを追加
            </button>
          </form>
          {recording && (
            <p role="status">録画中はCameraの追加・削除が無効です。</p>
          )}
          <div className="camera-list">
            {cameras.map((camera) => (
              <CameraCard
                key={camera.name}
                camera={camera}
                urls={connectionURLs[camera.name] ?? camera.endpoints}
                disabled={recording || submitting}
                onDelete={() => void removeCamera(camera.name)}
              />
            ))}
          </div>
        </section>
        <Preview cameras={cameras} />
        {error !== null && (
          <p id="application-error" role="alert">
            {error}
          </p>
        )}
      </main>
    );
  }

  return (
    <main>
      <h1>Kinugasa Recording</h1>
      <p>新しい録画Sessionを作成します。</p>
      <form onSubmit={submitSession} noValidate>
        <label htmlFor="session-name">Session名</label>
        <input
          id="session-name"
          value={name}
          onChange={(event) => setName(event.target.value)}
          autoComplete="off"
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

function CameraCard({
  camera,
  urls,
  disabled,
  onDelete,
}: {
  camera: CameraStatus;
  urls?: { rist?: string; srt?: string };
  disabled: boolean;
  onDelete: () => void;
}) {
  return (
    <article className="camera-card">
      <div>
        <h3>{camera.name}</h3>
        <p>Status: {camera.phase}</p>
      </div>
      {urls && (
        <div className="connection-grid">
          {(["rist", "srt"] as const).map(
            (protocol) =>
              urls[protocol] && (
                <div key={protocol}>
                  <h4>{protocol.toUpperCase()}</h4>
                  <code>{urls[protocol]}</code>
                  <QRCodeSVG
                    value={urls[protocol] ?? ""}
                    size={144}
                    title={`${camera.name} ${protocol.toUpperCase()} connection URL`}
                  />
                </div>
              ),
          )}
        </div>
      )}
      <button
        className="danger"
        type="button"
        onClick={onDelete}
        disabled={disabled || camera.phase === "Deleting"}
      >
        Cameraを削除
      </button>
    </article>
  );
}
