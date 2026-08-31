import { Camera, History, Plus, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import type { PreviewAccess } from "../api/types";
import { AppShell } from "../components/AppShell";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { ErrorBanner } from "../components/ErrorBanner";
import { PageLoading } from "../components/PageLoading";
import { ResourceNameForm } from "../components/ResourceNameForm";
import { CameraCard } from "../features/cameras/CameraCard";
import { PreviewGrid } from "../features/preview/PreviewGrid";
import { TakeControls } from "../features/takes/TakeControls";
import { TakeListItem } from "../features/takes/TakeListItem";
import { usePollingResource } from "../hooks/usePollingResource";

export function SessionConsolePage() {
  const { sessionName = "" } = useParams();
  const [actionError, setActionError] = useState<Error | null>(null);
  const [previewAccess, setPreviewAccess] = useState<PreviewAccess | null>(null);
  const loadSession = useCallback(() => api.getSession(sessionName), [sessionName]);
  const loadCameras = useCallback(() => api.listCameras(sessionName), [sessionName]);
  const loadOngoing = useCallback(() => api.getOngoingTake(sessionName), [sessionName]);
  const loadTakes = useCallback(() => api.listTakes(sessionName, 1, 100), [sessionName]);
  const session = usePollingResource(loadSession, 5000);
  const cameras = usePollingResource(loadCameras, 2000);
  const ongoing = usePollingResource(loadOngoing, 1500);
  const takes = usePollingResource(loadTakes, 3000);

  useEffect(() => {
    let active = true;
    let timer = 0;
    async function refreshAccess() {
      try {
        const access = await api.createPreviewAccess(sessionName);
        if (!active) return;
        setPreviewAccess(access);
        const refreshAfter = Math.max(30_000, Date.parse(access.expiresAt) - Date.now() - 60_000);
        timer = window.setTimeout(() => void refreshAccess(), refreshAfter);
      } catch (error) {
        if (active) setActionError(error instanceof Error ? error : new Error(String(error)));
      }
    }
    void refreshAccess();
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [sessionName]);

  async function mutate(operation: () => Promise<unknown>) {
    setActionError(null);
    try {
      await operation();
      await Promise.all([session.reload(), cameras.reload(), ongoing.reload(), takes.reload()]);
    } catch (error) {
      setActionError(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }
  }

  const uploading = takes.data?.items.filter((take) => take.state === "uploading") ?? [];
  async function prepareCameraDeletion(cameraName: string): Promise<string[]> {
    setActionError(null);
    try {
      const page = await api.listTakes(sessionName, 1, 100);
      const uploadingTakes = page.items.filter((take) => take.state === "uploading");
      const details = await Promise.all(uploadingTakes.map((take) => api.getTake(sessionName, take.name)));
      return details
        .filter((take) => take.videoFiles.some((file) => file.cameraName === cameraName && file.state === "uploading"))
        .map((take) => take.name);
    } catch (error) {
      setActionError(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }
  }

  if ((session.loading || cameras.loading || ongoing.loading) && (!session.data || !cameras.data || !ongoing.data)) {
    return <AppShell sessionName={sessionName}><PageLoading /></AppShell>;
  }
  const hasOngoing = ongoing.data?.type === "present";

  return (
    <AppShell sessionName={sessionName}>
      <header className="console-header">
        <div><span className="eyebrow">Session console</span><h1>{session.data?.name ?? sessionName}</h1><p>{session.data?.id}</p></div>
        <Button variant="quiet" icon={<RefreshCw size={16} />} onClick={() => void Promise.all([cameras.reload(), ongoing.reload(), takes.reload()])}>更新</Button>
      </header>
      <ErrorBanner error={actionError ?? session.error ?? cameras.error ?? ongoing.error ?? takes.error} onDismiss={() => setActionError(null)} />
      <section className="console-layout">
        <div className="console-main">
          <section className="panel preview-panel">
            <div className="panel-heading"><div><span className="eyebrow">Live preview</span><h2>Camera feeds</h2></div><span className="panel-count">{cameras.data?.filter((camera) => camera.status === "connected").length ?? 0} / {cameras.data?.length ?? 0} online</span></div>
            <PreviewGrid cameras={cameras.data ?? []} access={previewAccess} />
          </section>
          <section className="panel cameras-panel">
            <div className="panel-heading"><div className="section-icon"><Camera size={19} /></div><div><h2>Cameras</h2><p>RIST接続状態と送信先を管理します。</p></div></div>
            <ResourceNameForm
              label="Camera name"
              placeholder="camera-a"
              actionLabel="Cameraを追加"
              disabled={hasOngoing}
              onSubmit={(name) => mutate(() => api.createCamera(sessionName, name))}
            />
            <div className="camera-list">
              {(cameras.data ?? []).map((camera) => (
                <CameraCard
                  key={camera.name}
                  sessionName={sessionName}
                  camera={camera}
                  deletionDisabled={hasOngoing}
                  onPrepareDelete={prepareCameraDeletion}
                  onDelete={(name, force) => mutate(() => api.deleteCamera(sessionName, name, force))}
                />
              ))}
              {cameras.data?.length === 0 && <EmptyState icon={<Plus size={24} />} title="Camera未登録" description="Cameraを追加すると接続先が割り当てられます。" />}
            </div>
          </section>
        </div>
        <aside className="console-side">
          <section className="panel take-panel">
            {ongoing.data && (
              <TakeControls
                sessionName={sessionName}
                cameras={cameras.data ?? []}
                ongoing={ongoing.data}
                onStart={(name, selected) => mutate(() => api.startTake(sessionName, name, selected))}
                onFinish={() => mutate(() => api.finishTake(sessionName))}
              />
            )}
          </section>
          <section className="panel uploading-panel">
            <div className="panel-heading compact"><div className="section-icon"><History size={18} /></div><div><h2>Uploading</h2><p>録画停止後の転送状況</p></div></div>
            {uploading.length ? uploading.map((take) => <TakeListItem key={take.id} sessionName={sessionName} take={take} />) : <p className="muted padded">アップロード中のTakeはありません。</p>}
            <Link className="text-link" to={`/sessions/${encodeURIComponent(sessionName)}/takes`}>すべてのTakeを見る →</Link>
          </section>
        </aside>
      </section>
    </AppShell>
  );
}
