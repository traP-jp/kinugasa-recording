export function previewGridColumnCount(itemCount: number): number {
  return Math.ceil(Math.sqrt(Math.max(1, itemCount)));
}
