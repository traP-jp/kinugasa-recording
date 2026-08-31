import { CircleStop, Radio, Video } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import type { CameraConnection, OngoingTakeResult } from "../../api/types";
import { Button } from "../../components/Button";
import { StatusBadge } from "../../components/StatusBadge";
import { formatDateTime } from "../../lib/format";
import { loadLastTakeCameras, storeLastTakeCameras } from "../../lib/takeCameraSelection";
import { CameraSelectionActions } from "./CameraSelectionActions";

interface TakeControlsProps {
  sessionName: string;
  cameras: CameraConnection[];
  ongoing: OngoingTakeResult;
  onStart: (name: string, cameras: string[]) => Promise<void>;
  onFinish: () => Promise<void>;
}

export function TakeControls({ sessionName, cameras, ongoing, onStart, onFinish }: TakeControlsProps) {
  const connected = useMemo(() => cameras.filter((camera) => camera.status === "connected"), [cameras]);
  const [name, setName] = useState("");
  const [selected, setSelected] = useState<string[]>(() => loadLastTakeCameras(sessionName));
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setSelected(loadLastTakeCameras(sessionName));
  }, [sessionName]);
  useEffect(() => {
    setSelected((current) => current.filter((item) => connected.some((camera) => camera.name === item)));
  }, [connected]);

  if (ongoing.type === "present") {
    const take = ongoing.ongoingTake;
    return (
      <div className="take-active">
        <div className="recording-beacon"><span /><Radio size={22} /></div>
        <div className="take-active-main">
          <span className="eyebrow">Recording now</span>
          <h3>{take.name}</h3>
          <p>開始 {formatDateTime(take.startedAt)}</p>
          <div className="recording-camera-list">
            {take.cameras.map((camera) => (
              <span key={camera.name}><StatusBadge status={camera.state} />{camera.name}</span>
            ))}
          </div>
        </div>
        <Button
          variant="danger"
          icon={<CircleStop size={18} />}
          disabled={busy}
          onClick={() => {
            setBusy(true);
            void onFinish().finally(() => setBusy(false));
          }}
        >{busy ? "停止中…" : "録画を停止"}</Button>
      </div>
    );
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!name || selected.length === 0) return;
    setBusy(true);
    try {
      await onStart(name, selected);
      storeLastTakeCameras(sessionName, selected);
      setName("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="take-start" onSubmit={(event) => void submit(event)}>
      <div className="take-start-heading">
        <div className="section-icon"><Video size={19} /></div>
        <div><h3>新しいTake</h3><p>接続済みCameraを選択して録画を開始します。</p></div>
      </div>
      <label className="take-name-field"><span>Take name</span><input value={name} onChange={(event) => setName(event.target.value)} placeholder="take-001" maxLength={32} /></label>
      <fieldset>
        <legend>Recording cameras</legend>
        {connected.length > 0 && (
          <CameraSelectionActions
            allSelected={selected.length === connected.length}
            noneSelected={selected.length === 0}
            onSelectAll={() => setSelected(connected.map((camera) => camera.name))}
            onClear={() => setSelected([])}
          />
        )}
        {connected.length === 0 ? <p className="muted">接続済みのCameraがありません。</p> : connected.map((camera) => (
          <label className="camera-check" key={camera.name}>
            <input
              type="checkbox"
              checked={selected.includes(camera.name)}
              onChange={(event) => setSelected((current) => event.target.checked ? [...current, camera.name] : current.filter((name) => name !== camera.name))}
            />
            <span>{camera.name}</span><span className="signal-dot signal-connected" />
          </label>
        ))}
      </fieldset>
      <Button type="submit" variant="primary" icon={<Radio size={17} />} disabled={busy || !name || selected.length === 0}>
        {busy ? "開始中…" : "録画を開始"}
      </Button>
    </form>
  );
}
