import type { CameraConnection, CameraStatus, RISTStatistics } from "../../api/types";
import { hasPacketRecoveryFailure } from "./ristStatistics";

export type CameraDisplayStatus = CameraStatus | "packet-loss";

export interface CameraDisplayState {
  camera: CameraConnection;
  ristStatistics: RISTStatistics[];
  status: CameraDisplayStatus;
  packetRecoveryFailed: boolean;
}

export function deriveCameraDisplayState(
  camera: CameraConnection,
  ristStatistics: RISTStatistics[],
): CameraDisplayState {
  const activeRISTStatistics = ristStatistics.filter((item) => !item.stale);
  const packetRecoveryFailed = hasPacketRecoveryFailure(activeRISTStatistics);
  return {
    camera,
    ristStatistics: activeRISTStatistics,
    packetRecoveryFailed,
    status: packetRecoveryFailed ? "packet-loss" : camera.status,
  };
}

export function deriveCameraDisplayStates(
  cameras: CameraConnection[],
  ristStatistics: RISTStatistics[],
): CameraDisplayState[] {
  const statisticsByCamera = new Map<string, RISTStatistics[]>();
  for (const item of ristStatistics) {
    const statistics = statisticsByCamera.get(item.cameraName);
    if (statistics) {
      statistics.push(item);
    } else {
      statisticsByCamera.set(item.cameraName, [item]);
    }
  }
  return cameras.map((camera) => deriveCameraDisplayState(camera, statisticsByCamera.get(camera.name) ?? []));
}
