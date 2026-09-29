import type * as Moq from "@moq/net";
import { VideoOff } from "lucide-react";
import { useState, type KeyboardEvent, type ReactNode } from "react";
import type { PreviewAccess } from "../../api/types";
import { previewGridColumnCount } from "../../lib/previewGrid";
import type { CameraDisplayState } from "../cameras/cameraDisplayState";
import { AudioLevelMeter } from "./AudioLevelMeter";
import { MoqCameraPreview } from "./MoqCameraPreview";
import { VideoPreviewModal } from "./VideoPreviewModal";
import { useMoqConnection } from "./useMoqConnection";

interface PreviewGridProps {
  cameras: CameraDisplayState[];
  access: PreviewAccess | null;
}

export function PreviewGrid({ cameras, access }: PreviewGridProps) {
  if (!access) {
    return <PreviewPlaceholders cameras={cameras} message="プレビュー接続情報を取得しています" />;
  }
  return <MoqPreviewSession cameras={cameras} access={access} />;
}

function MoqPreviewSession({ cameras, access }: PreviewGridProps & { access: PreviewAccess }) {
  const { connection, status, failed } = useMoqConnection(access);
  if (status === "unsupported") {
    return <PreviewPlaceholders cameras={cameras} message="このブラウザはMedia over QUICに対応していません" />;
  }
  if (status === "invalid" || failed) {
    return <PreviewPlaceholders cameras={cameras} message="Media over QUICへ接続できません" />;
  }
  return <ConnectedPreviewGrid cameras={cameras} connection={connection} />;
}

function ConnectedPreviewGrid({
  cameras,
  connection,
}: {
  cameras: CameraDisplayState[];
  connection?: Moq.Connection;
}) {
  const [expandedCameraName, setExpandedCameraName] = useState<string | null>(null);
  if (cameras.length === 0) return <PreviewPlaceholders cameras={[]} message="Cameraを追加すると映像が表示されます" />;
  const expandedCamera = cameras.find((camera) => camera.camera.name === expandedCameraName);
  return (
    <>
      <PreviewTileGrid itemCount={cameras.length}>
        {cameras.map((cameraDisplay) => {
          const camera = cameraDisplay.camera;
          return (
            <PreviewTile key={camera.name} cameraName={camera.name} onOpen={() => setExpandedCameraName(camera.name)}>
              <MoqCameraPreview camera={camera} connection={connection} />
              <PreviewLabel cameraDisplay={cameraDisplay} />
            </PreviewTile>
          );
        })}
      </PreviewTileGrid>
      {expandedCamera && (
        <VideoPreviewModal cameraName={expandedCamera.camera.name} onClose={() => setExpandedCameraName(null)}>
          <div className="video-preview-expanded">
            <MoqCameraPreview camera={expandedCamera.camera} connection={connection} />
          </div>
        </VideoPreviewModal>
      )}
    </>
  );
}

function PreviewPlaceholders({ cameras, message }: { cameras: CameraDisplayState[]; message: string }) {
  const [expandedCameraName, setExpandedCameraName] = useState<string | null>(null);
  const expandedCamera = cameras.find((camera) => camera.camera.name === expandedCameraName);
  return (
    <>
      <PreviewTileGrid itemCount={cameras.length}>
        {cameras.length ? cameras.map((cameraDisplay) => (
          <PreviewTile key={cameraDisplay.camera.name} cameraName={cameraDisplay.camera.name} onOpen={() => setExpandedCameraName(cameraDisplay.camera.name)}>
            <PreviewWaiting message={message} />
            <PreviewLabel cameraDisplay={cameraDisplay} />
            <AudioLevelMeter cameraName={cameraDisplay.camera.name} />
          </PreviewTile>
        )) : (
          <article className="preview-tile"><PreviewWaiting message={message} /></article>
        )}
      </PreviewTileGrid>
      {expandedCamera && (
        <VideoPreviewModal cameraName={expandedCamera.camera.name} onClose={() => setExpandedCameraName(null)}>
          <div className="video-preview-expanded">
            <PreviewWaiting message={message} />
            <AudioLevelMeter cameraName={expandedCamera.camera.name} />
          </div>
        </VideoPreviewModal>
      )}
    </>
  );
}

function PreviewLabel({ cameraDisplay }: { cameraDisplay: CameraDisplayState }) {
  return (
    <div className="preview-label">
      <span className={`signal-dot signal-${cameraDisplay.status}`} />
      {cameraDisplay.camera.name}
    </div>
  );
}

function PreviewWaiting({ message }: { message: string }) {
  return <div className="preview-waiting"><VideoOff size={28} /><span>{message}</span></div>;
}

function PreviewTile({ cameraName, children, onOpen }: { cameraName: string; children: ReactNode; onOpen: () => void }) {
  function openWithKeyboard(event: KeyboardEvent<HTMLElement>) {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    onOpen();
  }
  return (
    <article
      className="preview-tile"
      role="button"
      tabIndex={0}
      aria-label={`${cameraName}を拡大表示`}
      onClick={onOpen}
      onKeyDown={openWithKeyboard}
    >
      {children}
    </article>
  );
}

function PreviewTileGrid({ itemCount, children }: { itemCount: number; children: ReactNode }) {
  const columns = previewGridColumnCount(itemCount);
  return (
    <div className="preview-grid" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
      {children}
    </div>
  );
}
