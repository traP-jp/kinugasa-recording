import * as Watch from "@moq/watch";
import { SignalZero, VideoOff } from "lucide-react";
import { useEffect, useState } from "react";
import type { CameraConnection } from "../../api/types";
import { AudioLevelMeter } from "./AudioLevelMeter";
import { useReceivedAudioLevel } from "./useReceivedAudioLevel";

interface MoqCameraPreviewProps {
  camera: CameraConnection;
  connection?: Watch.Net.Connection;
  visible?: Watch.Video.Visible;
}

type BroadcastStatus = "offline" | "loading" | "live";

export function MoqCameraPreview({
  camera,
  connection,
  visible = "20%",
}: MoqCameraPreviewProps) {
  const [canvas, setCanvas] = useState<HTMLCanvasElement | null>(null);
  const [player, setPlayer] = useState<Watch.Player>();
  const [broadcastStatus, setBroadcastStatus] = useState<BroadcastStatus>("offline");
  const [hasVideo, setHasVideo] = useState(false);
  const audioLevel = useReceivedAudioLevel(player);
  const active = camera.status === "connected";

  useEffect(() => {
    setPlayer(undefined);
    setBroadcastStatus("offline");
    setHasVideo(false);
    if (!connection || !canvas || !active) return;

    const nextPlayer = new Watch.Player({
      origin: connection.origin,
      probe: connection.probe,
      name: Watch.Net.Path.from(camera.name),
      canvas,
      announced: true,
      catalogFormat: "hang",
      visible,
      // `muted` disables audio download; zero gain keeps decoding active for the meter.
      muted: false,
      volume: 0,
    });
    setBroadcastStatus(nextPlayer.broadcast.out.status.peek());
    setHasVideo((nextPlayer.video.out.stats.peek()?.frameCount ?? 0) > 0);
    const unsubscribeStatus = nextPlayer.broadcast.out.status.subscribe(setBroadcastStatus);
    const unsubscribeVideo = nextPlayer.video.out.stats.subscribe((stats) => {
      setHasVideo((current) => current || (stats?.frameCount ?? 0) > 0);
    });
    setPlayer(nextPlayer);

    return () => {
      unsubscribeStatus();
      unsubscribeVideo();
      nextPlayer.close();
    };
  }, [active, camera.name, canvas, connection, visible]);

  return (
    <div className="moq-camera-preview">
      <canvas ref={setCanvas} aria-label={`${camera.name}のライブ映像`} />
      {!hasVideo && (
        <div className="preview-waiting">
          {active ? <SignalZero size={28} /> : <VideoOff size={28} />}
          <span>{previewMessage(active, connection !== undefined, broadcastStatus)}</span>
        </div>
      )}
      <AudioLevelMeter cameraName={camera.name} measurement={audioLevel} />
    </div>
  );
}

function previewMessage(
  active: boolean,
  hasConnection: boolean,
  status: BroadcastStatus,
): string {
  if (!active) return "映像信号なし";
  if (!hasConnection) return "Media over QUICへ接続しています";
  if (status === "loading") return "MoQストリームを読み込み中";
  if (status === "live") return "映像トラックを待機中";
  return "MoQストリームを待機中";
}
