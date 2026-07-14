export interface CameraSpec {
  name: string;
  desiredState: "Present" | "Absent";
}

export interface CameraStatus {
  name: string;
  phase:
    | "Provisioning"
    | "Waiting"
    | "Connected"
    | "Disconnected"
    | "Deleting"
    | "Removed"
    | "Error";
  endpoints?: { rist?: string; srt?: string };
}

export interface SessionResource {
  name: string;
  spec: {
    cameras: CameraSpec[];
    takes: {
      name: string;
      desiredState: "Recording" | "Stopped";
      cameraNames: string[];
    }[];
  };
  status: { cameras: CameraStatus[]; takes: TakeStatus[] };
}

export interface TakeStatus {
  name: string;
  phase:
    | "Pending"
    | "Starting"
    | "Recording"
    | "Stopping"
    | "Uploading"
    | "Completed"
    | "Failed";
  cameras?: {
    name: string;
    recorderPhase?: string;
    uploadPhase?: string;
    pendingFiles?: number;
    failedFiles?: number;
    conditions?: {
      type: string;
      status: "True" | "False" | "Unknown";
      reason: string;
      message: string;
    }[];
  }[];
}

export interface TakeMutation {
  take: { name: string; phase: TakeStatus["phase"]; cameraNames: string[] };
  excludedCameras?: { name: string; reason: string }[];
}

export interface CameraMutation {
  camera: { name: string; phase: CameraStatus["phase"] };
  connectionUrls?: { rist?: string; srt?: string };
}

export interface PreviewToken {
  serverUrl: string;
  roomName: string;
  participantToken: string;
  expiresAt: string;
}

interface ErrorResponse {
  error?: { code?: string; message?: string };
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

function normalizeSession(
  session: Partial<SessionResource> & { name: string },
) {
  return {
    name: session.name,
    spec: {
      cameras: session.spec?.cameras ?? [],
      takes: session.spec?.takes ?? [],
    },
    status: {
      cameras: session.status?.cameras ?? [],
      takes: session.status?.takes ?? [],
    },
  } satisfies SessionResource;
}

async function jsonRequest<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init);
  const body = (await response.json()) as T & ErrorResponse;
  if (!response.ok) {
    throw new ApiError(
      response.status,
      body.error?.code ?? "INTERNAL",
      body.error?.message ?? "request failed",
    );
  }
  return body;
}

export async function createSession(name: string): Promise<SessionResource> {
  const body = await jsonRequest<{ session: SessionResource }>(
    "/api/v1/sessions",
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": crypto.randomUUID(),
      },
      body: JSON.stringify({ name }),
    },
  );
  return normalizeSession(body.session);
}

export async function getSession(name: string): Promise<SessionResource> {
  const body = await jsonRequest<{ session: SessionResource }>(
    `/api/v1/sessions/${encodeURIComponent(name)}`,
  );
  return normalizeSession(body.session);
}

export async function addCamera(
  session: string,
  name: string,
): Promise<CameraMutation> {
  return jsonRequest(
    `/api/v1/sessions/${encodeURIComponent(session)}/cameras`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": crypto.randomUUID(),
      },
      body: JSON.stringify({ name }),
    },
  );
}

export async function deleteCamera(
  session: string,
  name: string,
): Promise<CameraMutation> {
  return jsonRequest(
    `/api/v1/sessions/${encodeURIComponent(session)}/cameras/${encodeURIComponent(name)}`,
    {
      method: "DELETE",
      headers: { "Idempotency-Key": crypto.randomUUID() },
    },
  );
}

export function getPreviewToken(): Promise<PreviewToken> {
  return jsonRequest("/api/v1/livekit/token", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
}

export function startTake(
  session: string,
  name: string,
  cameraNames: string[],
): Promise<TakeMutation> {
  return jsonRequest(`/api/v1/sessions/${encodeURIComponent(session)}/takes`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ name, cameraNames }),
  });
}

export function stopTake(session: string, name: string): Promise<TakeMutation> {
  return jsonRequest(
    `/api/v1/sessions/${encodeURIComponent(session)}/takes/${encodeURIComponent(name)}/stop`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": crypto.randomUUID(),
      },
      body: "{}",
    },
  );
}
