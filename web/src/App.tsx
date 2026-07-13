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
  startTake,
  stopTake,
} from "./api";
import { Preview } from "./Preview";

const namePattern = /^[A-Za-z0-9-]+$/;

function validateName(
  name: string,
  kind: "Session" | "Camera" | "Take",
): string | null {
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
  const [takeName, setTakeName] = useState("");
  const [selectedCameras, setSelectedCameras] = useState<string[]>([]);
  const [takeWarnings, setTakeWarnings] = useState<string[]>([]);
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
          ...session.status,
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
          ...session.status,
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

  async function submitTake(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (sessionName === null || session === null) return;
    const validationError = validateName(takeName, "Take");
    if (validationError !== null) {
      setError(validationError);
      return;
    }
    setSubmitting(true);
    setError(null);
    setTakeWarnings([]);
    try {
      const started = await startTake(sessionName, takeName, selectedCameras);
      setTakeWarnings(
        (started.excludedCameras ?? []).map(
          (camera) => `${camera.name}: ${camera.reason}`,
        ),
      );
      setSession({
        ...session,
        spec: {
          ...session.spec,
          takes: [
            ...session.spec.takes,
            {
              name: takeName,
              desiredState: "Recording",
              cameraNames: started.take.cameraNames,
            },
          ],
        },
        status: {
          ...session.status,
          takes: [
            ...session.status.takes,
            { name: takeName, phase: "Pending" },
          ],
        },
      });
      setTakeName("");
      setSelectedCameras([]);
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "NAME_RESERVED")
        setError("このTake名は現在または過去に使用されています。");
      else if (
        caught instanceof ApiError &&
        caught.code === "NO_AVAILABLE_CAMERA"
      )
        setError("録画可能なCameraがありません。");
      else setError("Takeを開始できませんでした。");
    } finally {
      setSubmitting(false);
    }
  }

  async function requestTakeStop(take: string) {
    if (sessionName === null || session === null) return;
    setSubmitting(true);
    setError(null);
    try {
      await stopTake(sessionName, take);
      setSession({
        ...session,
        spec: {
          ...session.spec,
          takes: session.spec.takes.map((item) =>
            item.name === take ? { ...item, desiredState: "Stopped" } : item,
          ),
        },
        status: {
          ...session.status,
          takes: session.status.takes.map((item) =>
            item.name === take ? { ...item, phase: "Stopping" } : item,
          ),
        },
      });
    } catch {
      setError("Takeを停止できませんでした。");
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
    const takeStatuses = session?.status.takes ?? [];
    const activeTake = session?.spec.takes.find(
      (take) => take.desiredState === "Recording",
    );
    const activeStatus = activeTake
      ? takeStatuses.find((take) => take.name === activeTake.name)
      : undefined;
    const disconnectedRecordingCameras =
      activeTake?.cameraNames.filter(
        (name) =>
          cameras.find((camera) => camera.name === name)?.phase !== "Connected",
      ) ?? [];
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
        <section aria-labelledby="take-heading">
          <h2 id="take-heading">Takes</h2>
          {!recording && (
            <form onSubmit={submitTake} noValidate>
              <label htmlFor="take-name">Take名</label>
              <input
                id="take-name"
                value={takeName}
                onChange={(event) => setTakeName(event.target.value)}
                disabled={submitting}
              />
              <fieldset>
                <legend>録画するCamera（未選択の場合は全Camera）</legend>
                {cameras.map((camera) => (
                  <label key={camera.name} className="checkbox">
                    <input
                      type="checkbox"
                      checked={selectedCameras.includes(camera.name)}
                      onChange={(event) =>
                        setSelectedCameras((current) =>
                          event.target.checked
                            ? [...current, camera.name]
                            : current.filter((name) => name !== camera.name),
                        )
                      }
                    />
                    {camera.name} ({camera.phase})
                  </label>
                ))}
              </fieldset>
              <button
                type="submit"
                disabled={submitting || cameras.length === 0}
              >
                Takeを開始
              </button>
            </form>
          )}
          {activeTake && (
            <article className="take-active">
              <h3>{activeTake.name}</h3>
              <p>Status: {activeStatus?.phase ?? "Pending"}</p>
              <p>Camera: {activeTake.cameraNames.join(", ")}</p>
              <button
                type="button"
                className="danger"
                disabled={submitting}
                onClick={() => void requestTakeStop(activeTake.name)}
              >
                Takeを停止
              </button>
            </article>
          )}
          {takeStatuses.length > 0 && (
            <ul className="take-list">
              {takeStatuses.map((take) => (
                <li key={take.name}>
                  {take.name}: {take.phase}
                  {(take.cameras ?? []).map((camera) => (
                    <span key={camera.name}>
                      {" "}
                      {camera.name} (Recorder:{" "}
                      {camera.recorderPhase ?? "Pending"}, Upload:{" "}
                      {camera.uploadPhase ?? "Pending"})
                    </span>
                  ))}
                </li>
              ))}
            </ul>
          )}
          {takeWarnings.map((warning) => (
            <p role="alert" key={warning}>
              除外Camera: {warning}
            </p>
          ))}
          {disconnectedRecordingCameras.length > 0 && (
            <p role="alert">
              録画中にCameraが切断されています:{" "}
              {disconnectedRecordingCameras.join(", ")}
            </p>
          )}
          {takeStatuses
            .flatMap((take) => take.cameras ?? [])
            .filter((camera) => camera.uploadPhase === "Failed")
            .map((camera) => (
              <p role="alert" key={`upload-${camera.name}`}>
                Upload失敗: {camera.name}
              </p>
            ))}
          {takeStatuses
            .filter((take) => take.phase === "Uploading")
            .map((take) => (
              <p role="status" key={`uploading-${take.name}`}>
                Upload完了待ち: {take.name}
              </p>
            ))}
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
