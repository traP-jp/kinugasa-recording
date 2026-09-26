import { describe, expect, it } from "vitest";
import { loadLastTakeCameras, storeLastTakeCameras } from "./takeCameraSelection";

function createStorage(initial: Record<string, string> = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => void values.set(key, value),
  };
}

describe("last take camera selection", () => {
  it("stores selections separately for each session", () => {
    const storage = createStorage();

    storeLastTakeCameras("session-a", ["camera-1", "camera-2"], storage);
    storeLastTakeCameras("session-b", ["camera-3"], storage);

    expect(loadLastTakeCameras("session-a", storage)).toEqual(["camera-1", "camera-2"]);
    expect(loadLastTakeCameras("session-b", storage)).toEqual(["camera-3"]);
  });

  it("ignores malformed, non-string, and duplicate values", () => {
    const storage = createStorage({
      "kinugasa-recording:last-take-cameras:session-a": "[\"camera-1\",42,\"camera-1\"]",
      "kinugasa-recording:last-take-cameras:session-b": "invalid",
    });

    expect(loadLastTakeCameras("session-a", storage)).toEqual(["camera-1"]);
    expect(loadLastTakeCameras("session-b", storage)).toEqual([]);
  });
});
