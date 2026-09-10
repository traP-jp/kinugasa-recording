import { useTrackVolume, type TrackReference } from "@livekit/components-react";

interface AudioLevelMeterProps {
  cameraName: string;
  track?: TrackReference;
}

const analyserOptions = {
  fftSize: 64,
  smoothingTimeConstant: 0.7,
  minDecibels: -60,
  maxDecibels: -10,
};

export function AudioLevelMeter({ cameraName, track }: AudioLevelMeterProps) {
  const volume = useTrackVolume(track, analyserOptions);
  const available = track?.publication.track !== undefined;
  const percentage = available ? Math.round(Math.min(1, Math.max(0, volume)) * 100) : 0;

  return (
    <div
      className={`audio-level-meter${available ? "" : " audio-level-meter-unavailable"}`}
      role="meter"
      aria-label={`${cameraName}の音量`}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percentage}
      aria-valuetext={available ? `${percentage}%` : "音声トラックなし"}
    >
      <span className="audio-level-label">{available ? "AUDIO" : "NO AUDIO"}</span>
      <span className="audio-level-track">
        <span className="audio-level-fill" style={{ transform: `scaleX(${percentage / 100})` }} />
      </span>
    </div>
  );
}
