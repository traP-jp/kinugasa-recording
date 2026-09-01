import { LiveKitRoom, VideoTrack, useTracks } from "@livekit/components-react";
import { SignalZero, VideoOff } from "lucide-react";
import { Track } from "livekit-client";
import type { ReactNode } from "react";
import type { CameraConnection, PreviewAccess } from "../../api/types";
import { previewGridColumnCount } from "../../lib/previewGrid";

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
  const tracks = useTracks([Track.Source.Camera], { onlySubscribed: false });
  const tracksByIdentity = new Map(tracks.map((track) => [track.participant.identity, track]));
  if (cameras.length === 0) return <PreviewPlaceholders cameras={[]} message="Cameraを追加すると映像が表示されます" />;
  return (
    <PreviewTileGrid itemCount={cameras.length}>
      {cameras.map((camera) => {
        const track = tracksByIdentity.get(camera.name);
        return (
          <article className="preview-tile" key={camera.name}>
            {track ? <VideoTrack trackRef={track} /> : (
              <div className="preview-waiting">
                {camera.status === "connected" ? <SignalZero size={28} /> : <VideoOff size={28} />}
                <span>{camera.status === "connected" ? "LiveKit trackを待機中" : "映像信号なし"}</span>
              </div>
            )}
            <div className="preview-label"><span className={`signal-dot signal-${camera.status}`} />{camera.name}</div>
          </article>
        );
      })}
    </PreviewTileGrid>
  );
}

function PreviewPlaceholders({ cameras, message }: { cameras: CameraConnection[]; message: string }) {
  return (
    <PreviewTileGrid itemCount={cameras.length}>
      {(cameras.length ? cameras : [{ name: "preview" }]).map((camera) => (
        <article className="preview-tile" key={camera.name}>
          <div className="preview-waiting"><VideoOff size={28} /><span>{message}</span></div>
          {camera.name !== "preview" && <div className="preview-label">{camera.name}</div>}
        </article>
      ))}
    </PreviewTileGrid>
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
