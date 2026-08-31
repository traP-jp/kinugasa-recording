import type {
  CameraConnection,
  FinishedTake,
  FinishedTakeDetail,
  FinishedTakePage,
  OngoingTake,
  OngoingTakeResult,
  PreviewAccess,
  Session,
  SessionDetail,
  SessionPage,
} from "./types";

export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "APIError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api${path}`, {
    ...init,
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  if (!response.ok) {
    let message = `リクエストに失敗しました (${response.status})`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // Keep the status-based fallback when the server did not return JSON.
    }
    throw new APIError(message, response.status);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

const segment = encodeURIComponent;

export const api = {
  listSessions: (page = 1, pageSize = 20) =>
    request<SessionPage>(`/sessions?page=${page}&pageSize=${pageSize}`),
  createSession: (name: string) =>
    request<Session>("/sessions", { method: "POST", body: JSON.stringify({ name }) }),
  getSession: (sessionName: string) => request<SessionDetail>(`/sessions/${segment(sessionName)}`),
  listCameras: (sessionName: string) =>
    request<CameraConnection[]>(`/sessions/${segment(sessionName)}/cameras`),
  createCamera: (sessionName: string, name: string) =>
    request<CameraConnection>(`/sessions/${segment(sessionName)}/cameras`, {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  deleteCamera: (sessionName: string, cameraName: string, force = false) =>
	request<void>(`/sessions/${segment(sessionName)}/cameras/${segment(cameraName)}${force ? "?force=true" : ""}`, {
      method: "DELETE",
    }),
  getOngoingTake: (sessionName: string) =>
    request<OngoingTakeResult>(`/sessions/${segment(sessionName)}/ongoing-take`),
  startTake: (sessionName: string, name: string, cameraNames: string[]) =>
    request<OngoingTake>(`/sessions/${segment(sessionName)}/ongoing-take/start`, {
      method: "POST",
      body: JSON.stringify({ name, cameraNames }),
    }),
  finishTake: (sessionName: string) =>
    request<FinishedTake>(`/sessions/${segment(sessionName)}/ongoing-take/finish`, { method: "POST" }),
  listTakes: (sessionName: string, page = 1, pageSize = 20) =>
    request<FinishedTakePage>(
      `/sessions/${segment(sessionName)}/takes?page=${page}&pageSize=${pageSize}`,
    ),
  getTake: (sessionName: string, takeName: string) =>
    request<FinishedTakeDetail>(`/sessions/${segment(sessionName)}/takes/${segment(takeName)}`),
  createPreviewAccess: (sessionName: string) =>
    request<PreviewAccess>(`/sessions/${segment(sessionName)}/preview-access`, { method: "POST" }),
};
