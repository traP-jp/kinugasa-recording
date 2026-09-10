import { LiveKitRoom, VideoTrack, useTracks, type TrackReference } from "@livekit/components-react";
import { SignalZero, VideoOff } from "lucide-react";
import { Track } from "livekit-client";
import { useState, type KeyboardEvent, type ReactNode } from "react";
import type { CameraConnection, PreviewAccess } from "../../api/types";
import { previewGridColumnCount } from "../../lib/previewGrid";
import type { CameraDisplayState } from "../cameras/cameraDisplayState";
import { AudioLevelMeter } from "./AudioLevelMeter";
import { VideoPreviewModal } from "./VideoPreviewModal";

interface PreviewGridProps {
  cameras: CameraDisplayState[];
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

function ConnectedPreviewGrid({ cameras }: { cameras: CameraDisplayState[] }) {
  const [expandedCameraName, setExpandedCameraName] = useState<string | null>(null);
  const videoTracks = useTracks([Track.Source.Camera], { onlySubscribed: false });
  const audioTracks = useTracks([Track.Source.Microphone, Track.Source.Unknown], { onlySubscribed: false })
    .filter((track) => track.publication.kind === Track.Kind.Audio);
  const videoTracksByIdentity = new Map(videoTracks.map((track) => [track.participant.identity, track]));
  const audioTracksByIdentity = new Map(audioTracks.map((track) => [track.participant.identity, track]));
  if (cameras.length === 0) return <PreviewPlaceholders cameras={[]} message="Cameraを追加すると映像が表示されます" />;
  const expandedCamera = cameras.find((camera) => camera.camera.name === expandedCameraName);
  return (
    <>
      <PreviewTileGrid itemCount={cameras.length}>
        {cameras.map((cameraDisplay) => {
          const camera = cameraDisplay.camera;
          const videoTrack = videoTracksByIdentity.get(camera.name);
          const audioTrack = audioTracksByIdentity.get(camera.name);
          return (
            <PreviewTile key={camera.name} cameraName={camera.name} onOpen={() => setExpandedCameraName(camera.name)}>
              <CameraPreviewContent camera={camera} track={videoTrack} />
              <PreviewLabel cameraDisplay={cameraDisplay} />
              <AudioLevelMeter cameraName={camera.name} track={audioTrack} />
            </PreviewTile>
          );
        })}
      </PreviewTileGrid>
      {expandedCamera && (
        <VideoPreviewModal cameraName={expandedCamera.camera.name} onClose={() => setExpandedCameraName(null)}>
          <div className="video-preview-expanded">
            <CameraPreviewContent camera={expandedCamera.camera} track={videoTracksByIdentity.get(expandedCamera.camera.name)} />
            <AudioLevelMeter cameraName={expandedCamera.camera.name} track={audioTracksByIdentity.get(expandedCamera.camera.name)} />
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
