interface CameraSelectionStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

function storageKey(sessionName: string): string {
  return `kinugasa-recording:last-take-cameras:${sessionName}`;
}

export function loadLastTakeCameras(
  sessionName: string,
  storage: CameraSelectionStorage = window.localStorage,
): string[] {
  try {
    const value: unknown = JSON.parse(storage.getItem(storageKey(sessionName)) ?? "[]");
    if (!Array.isArray(value)) return [];
    return [...new Set(value.filter((camera): camera is string => typeof camera === "string"))];
  } catch {
    return [];
  }
}

export function storeLastTakeCameras(
  sessionName: string,
  cameras: string[],
  storage: CameraSelectionStorage = window.localStorage,
): void {
  try {
    storage.setItem(storageKey(sessionName), JSON.stringify(cameras));
  } catch {
    // Recording must still start when browser storage is unavailable.
  }
}
