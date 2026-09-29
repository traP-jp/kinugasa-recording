import type * as Moq from "@moq/net";
import { SignalZero, VideoOff } from "lucide-react";
import { useState } from "react";
import type { CameraConnection } from "../../api/types";
import { AudioLevelMeter } from "./AudioLevelMeter";
import { useMoqTsPlayer } from "./useMoqTsPlayer";
import { useReceivedAudioLevel } from "./useReceivedAudioLevel";

interface MoqCameraPreviewProps {
  camera: CameraConnection;
  connection?: Moq.Connection;
}

export function MoqCameraPreview({ camera, connection }: MoqCameraPreviewProps) {
  const [video, setVideo] = useState<HTMLVideoElement | null>(null);
  const active = camera.status === "connected";
  const { status, hasVideo, hasAudio } = useMoqTsPlayer(video, active ? connection : undefined, camera.name);
  const audioLevel = useReceivedAudioLevel(video, hasAudio);

  return (
    <div className="moq-camera-preview">
      <video ref={setVideo} aria-label={`${camera.name}のライブ映像`} muted playsInline />
      {!hasVideo && (
        <div className="preview-waiting">
          {active ? <SignalZero size={28} /> : <VideoOff size={28} />}
          <span>{previewMessage(active, connection !== undefined, status)}</span>
        </div>
      )}
      <AudioLevelMeter cameraName={camera.name} measurement={audioLevel} />
    </div>
  );
}

function previewMessage(active: boolean, hasConnection: boolean, status: string): string {
  if (!active) return "映像信号なし";
  if (!hasConnection) return "Media over QUICへ接続しています";
  if (status === "unsupported") return "このブラウザはMPEG-TS再生に対応していません";
  if (status === "error") return "映像を再生できません";
  if (status === "loading") return "MPEG-TSストリームを読み込み中";
  return "MoQストリームを待機中";
}
