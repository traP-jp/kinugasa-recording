import { LiveKitRoom, VideoTrack, useTracks, type TrackReference } from "@livekit/components-react";
import { SignalZero, VideoOff } from "lucide-react";
import { Track } from "livekit-client";
import { useState, type KeyboardEvent, type ReactNode } from "react";
import type { CameraConnection, PreviewAccess } from "../../api/types";
import { previewGridColumnCount } from "../../lib/previewGrid";
import { VideoPreviewModal } from "./VideoPreviewModal";

interface PreviewGridProps {
  cameras: CameraConnection[];
  access: PreviewAccess | null;
}

export function PreviewGrid({ cameras, access }: PreviewGridProps) {
  if (!access) {
    return <PreviewPlaceholders cameras={cameras} message="LiveKitへ接続しています" />;
  }
  return (
    <LiveKitRoom
      token={access.accessToken}
      serverUrl={access.url}
      connect
      audio={false}
      video={false}
      className="livekit-room"
    >
      <ConnectedPreviewGrid cameras={cameras} />
    </LiveKitRoom>
  );
}

function ConnectedPreviewGrid({ cameras }: { cameras: CameraConnection[] }) {
  const [expandedCameraName, setExpandedCameraName] = useState<string | null>(null);
  const tracks = useTracks([Track.Source.Camera], { onlySubscribed: false });
  const tracksByIdentity = new Map(tracks.map((track) => [track.participant.identity, track]));
  if (cameras.length === 0) return <PreviewPlaceholders cameras={[]} message="Cameraを追加すると映像が表示されます" />;
  const expandedCamera = cameras.find((camera) => camera.name === expandedCameraName);
  return (
    <>
      <PreviewTileGrid itemCount={cameras.length}>
        {cameras.map((camera) => {
          const track = tracksByIdentity.get(camera.name);
          return (
            <PreviewTile key={camera.name} cameraName={camera.name} onOpen={() => setExpandedCameraName(camera.name)}>
              <CameraPreviewContent camera={camera} track={track} />
              <div className="preview-label"><span className={`signal-dot signal-${camera.status}`} />{camera.name}</div>
            </PreviewTile>
          );
        })}
      </PreviewTileGrid>
      {expandedCamera && (
        <VideoPreviewModal cameraName={expandedCamera.name} onClose={() => setExpandedCameraName(null)}>
          <div className="video-preview-expanded">
            <CameraPreviewContent camera={expandedCamera} track={tracksByIdentity.get(expandedCamera.name)} />
          </div>
        </VideoPreviewModal>
      )}
    </>
  );
}

function PreviewPlaceholders({ cameras, message }: { cameras: CameraConnection[]; message: string }) {
  const [expandedCameraName, setExpandedCameraName] = useState<string | null>(null);
  const expandedCamera = cameras.find((camera) => camera.name === expandedCameraName);
  return (
    <>
      <PreviewTileGrid itemCount={cameras.length}>
        {cameras.length ? cameras.map((camera) => (
          <PreviewTile key={camera.name} cameraName={camera.name} onOpen={() => setExpandedCameraName(camera.name)}>
            <PreviewWaiting message={message} />
            <div className="preview-label">{camera.name}</div>
          </PreviewTile>
        )) : (
          <article className="preview-tile"><PreviewWaiting message={message} /></article>
        )}
      </PreviewTileGrid>
      {expandedCamera && (
        <VideoPreviewModal cameraName={expandedCamera.name} onClose={() => setExpandedCameraName(null)}>
          <div className="video-preview-expanded"><PreviewWaiting message={message} /></div>
        </VideoPreviewModal>
      )}
    </>
  );
}

function CameraPreviewContent({ camera, track }: { camera: CameraConnection; track?: TrackReference }) {
  if (track) return <VideoTrack trackRef={track} />;
  return (
    <div className="preview-waiting">
      {camera.status === "connected" ? <SignalZero size={28} /> : <VideoOff size={28} />}
      <span>{camera.status === "connected" ? "LiveKit trackを待機中" : "映像信号なし"}</span>
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
