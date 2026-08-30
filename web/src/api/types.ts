export type ResourceState = "active" | "inactive";
export type CameraStatus = "activating" | "waiting" | "connected" | "error";
export type TakeState = "uploading" | "completed" | "errored";

export interface Pagination {
  page: number;
  pageSize: number;
  total: number;
}

export interface Session {
  id: string;
  name: string;
  state: ResourceState;
  createdAt: string;
}

export interface SessionDetail extends Session {
  ongoingTakeName: string | null;
}

export interface SessionPage {
  items: Session[];
  pagination: Pagination;
}

export interface CameraConnection {
  name: string;
  url: string | null;
  status: CameraStatus;
  error: string | null;
}

export interface RecordingCamera {
  name: string;
  state: "recording" | "errored";
  startedAt: string;
  error: string | null;
}

export interface OngoingTake {
  id: string;
  name: string;
  startedAt: string;
  cameras: RecordingCamera[];
}

export type OngoingTakeResult =
  | { type: "absent" }
  | { type: "present"; ongoingTake: OngoingTake };

export interface FinishedTake {
  id: string;
  name: string;
  state: TakeState;
  startedAt: string;
  finishedAt: string;
  error: string | null;
}

export interface FinishedTakePage {
  items: FinishedTake[];
  pagination: Pagination;
}

export interface VideoFile {
  cameraName: string;
  state: TakeState;
  startedAt: string;
  finishedAt: string;
  objectKey: string | null;
  hash: string | null;
  error: string | null;
}

export interface FinishedTakeDetail extends FinishedTake {
  videoFiles: VideoFile[];
}

export interface PreviewAccess {
  url: string;
  accessToken: string;
  expiresAt: string;
}
