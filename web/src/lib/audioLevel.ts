export interface AudioEnergySample {
  audioLevel?: number;
  bytesReceived: number;
  totalAudioEnergy?: number;
  totalSamplesDuration?: number;
}

export function calculateAudioLevel(current: AudioEnergySample, previous?: AudioEnergySample): number {
  if (Number.isFinite(current.audioLevel)) return clampLevel(current.audioLevel ?? 0);
  if (!previous || current.totalAudioEnergy === undefined || current.totalSamplesDuration === undefined ||
    previous.totalAudioEnergy === undefined || previous.totalSamplesDuration === undefined) return 0;

  const energy = current.totalAudioEnergy - previous.totalAudioEnergy;
  const duration = current.totalSamplesDuration - previous.totalSamplesDuration;
  if (energy <= 0 || duration <= 0) return 0;
  return clampLevel(Math.sqrt(energy / duration));
}

export function audioLevelToPercentage(level: number): number {
  if (level <= 0 || !Number.isFinite(level)) return 0;
  const decibels = 20 * Math.log10(clampLevel(level));
  return Math.round(Math.min(1, Math.max(0, (decibels + 60) / 60)) * 100);
}

function clampLevel(level: number): number {
  return Math.min(1, Math.max(0, level));
}
