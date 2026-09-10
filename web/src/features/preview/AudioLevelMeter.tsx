import type { TrackReference } from "@livekit/components-react";
import { audioLevelToPercentage } from "../../lib/audioLevel";
import { useReceivedAudioLevel } from "./useReceivedAudioLevel";

interface AudioLevelMeterProps {
  cameraName: string;
  track?: TrackReference;
}

export function AudioLevelMeter({ cameraName, track }: AudioLevelMeterProps) {
  const { hasTrack, receiving, level } = useReceivedAudioLevel(track);
  const percentage = receiving ? audioLevelToPercentage(level) : 0;
  const label = receiving ? "AUDIO" : hasTrack ? "WAITING" : "NO AUDIO";
  const levelText = receiving ? `${percentage}%` : hasTrack ? "音声データ待機中" : "音声トラックなし";

  return (
    <div
      className={`audio-level-meter${receiving ? "" : " audio-level-meter-unavailable"}`}
      role="meter"
      aria-label={`${cameraName}の音量`}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percentage}
      aria-valuetext={levelText}
    >
      <span className="audio-level-label">{label}</span>
      <span className="audio-level-track">
        <span className="audio-level-fill" style={{ transform: `scaleX(${percentage / 100})` }} />
      </span>
    </div>
  );
}
