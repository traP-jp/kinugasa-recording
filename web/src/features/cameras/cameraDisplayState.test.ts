import { describe, expect, it } from "vitest";
import type { CameraConnection, RISTStatistics } from "../../api/types";
import { deriveCameraDisplayState, deriveCameraDisplayStates } from "./cameraDisplayState";

function camera(name: string, status: CameraConnection["status"]): CameraConnection {
  return { name, status, url: "rist://camera.example.com:9000", error: null };
}

function statistics(cameraName: string, lostPackets: number, stale = false): RISTStatistics {
  return {
    cameraName,
    gatewayInstance: "pod-uid",
    flowId: 42,
    intervalStart: "2026-09-06T12:00:00Z",
    intervalEnd: "2026-09-06T12:00:05Z",
    outputPackets: 100,
    lostPackets,
    recoveredPackets: 0,
    discontinuities: 0,
    stale,
  };
}

describe("deriveCameraDisplayState", () => {
  it("keeps the API camera status when no current packet recovery failure exists", () => {
    expect(deriveCameraDisplayState(camera("camera-1", "connected"), [statistics("camera-1", 0)]).status)
      .toBe("connected");
  });

  it("uses packet-loss as the UI status when recovery failed in a current interval", () => {
    const display = deriveCameraDisplayState(camera("camera-1", "connected"), [statistics("camera-1", 1)]);

    expect(display.status).toBe("packet-loss");
    expect(display.packetRecoveryFailed).toBe(true);
  });

  it("drops stale RIST statistics from the display state", () => {
    const display = deriveCameraDisplayState(
      camera("camera-1", "connected"),
      [statistics("camera-1", 2, true)],
    );

    expect(display.status).toBe("connected");
    expect(display.ristStatistics).toEqual([]);
  });

  it("groups RIST statistics by camera name", () => {
    const displays = deriveCameraDisplayStates(
      [camera("camera-1", "connected"), camera("camera-2", "connected")],
      [statistics("camera-2", 2), statistics("camera-1", 0), statistics("camera-2", 0)],
    );

    expect(displays.map((display) => [display.camera.name, display.status, display.ristStatistics.length]))
      .toEqual([["camera-1", "connected", 1], ["camera-2", "packet-loss", 2]]);
  });
});
