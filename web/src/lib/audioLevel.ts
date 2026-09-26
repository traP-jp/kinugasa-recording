export function audioLevelToPercentage(level: number): number {
  if (level <= 0 || !Number.isFinite(level)) return 0;
  const decibels = 20 * Math.log10(clampLevel(level));
  return Math.round(Math.min(1, Math.max(0, (decibels + 60) / 60)) * 100);
}

function clampLevel(level: number): number {
  return Math.min(1, Math.max(0, level));
}
