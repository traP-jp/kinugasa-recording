import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, vi } from "vitest";

import { App } from "./App";

vi.mock("livekit-client", () => ({
  Room: class {
    on() {
      return this;
    }
    connect() {
      return Promise.resolve();
    }
    disconnect() {}
  },
  RoomEvent: {
    TrackSubscribed: "trackSubscribed",
    TrackUnsubscribed: "trackUnsubscribed",
    Disconnected: "disconnected",
  },
  Track: { Kind: { Video: "video" } },
}));

describe("App", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    window.history.replaceState({}, "", "/");
  });

  it("renders the application title", () => {
    render(<App />);

    expect(
      screen.getByRole("heading", { name: "Kinugasa Recording" }),
    ).toBeInTheDocument();
  });

  it("creates a session and shows its screen", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: async () => ({ session: { name: "Session-1" } }),
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    fireEvent.change(screen.getByLabelText("Session名"), {
      target: { value: "Session-1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sessionを作成" }));

    expect(
      await screen.findByRole("heading", { name: "Session-1" }),
    ).toBeInTheDocument();
    expect(window.location.pathname).toBe("/sessions/Session-1");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/sessions",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("shows a warning for a previously used name", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 409,
        json: async () => ({
          error: { code: "NAME_RESERVED", message: "reserved" },
        }),
      }),
    );
    render(<App />);

    fireEvent.change(screen.getByLabelText("Session名"), {
      target: { value: "Session-1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sessionを作成" }));

    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent(
        "現在または過去に使用されています",
      );
    });
  });

  it("validates a session name before sending", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    fireEvent.change(screen.getByLabelText("Session名"), {
      target: { value: "invalid_name" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sessionを作成" }));

    expect(screen.getByRole("alert")).toHaveTextContent("英数字とハイフン");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("adds a camera and displays both connection URLs as QR codes", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) => {
      if (url === "/api/v1/sessions")
        return Promise.resolve({
          ok: true,
          status: 201,
          json: async () => ({ session: { name: "Session-1" } }),
        });
      if (url.endsWith("/cameras"))
        return Promise.resolve({
          ok: true,
          status: 202,
          json: async () => ({
            camera: { name: "front", phase: "Provisioning" },
            connectionUrls: {
              rist: "rist://host:31000",
              srt: "srt://host:31001?mode=caller&transtype=live",
            },
          }),
        });
      if (url === "/api/v1/livekit/token")
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({
            serverUrl: "wss://livekit",
            roomName: "preview",
            participantToken: "token",
            expiresAt: "2026-07-14T01:05:00Z",
          }),
        });
      throw new Error(`unexpected URL ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    fireEvent.change(screen.getByLabelText("Session名"), {
      target: { value: "Session-1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sessionを作成" }));
    await screen.findByRole("heading", { name: "Session-1" });
    fireEvent.change(screen.getByLabelText("Camera名"), {
      target: { value: "front" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Cameraを追加" }));
    expect(await screen.findByText("rist://host:31000")).toBeInTheDocument();
    expect(screen.getByTitle("front RIST connection URL")).toBeInTheDocument();
    expect(screen.getByTitle("front SRT connection URL")).toBeInTheDocument();
  });

  it("shows disconnected state and requests camera deletion", async () => {
    window.history.replaceState({}, "", "/sessions/Session-1");
    const fetchMock = vi
      .fn()
      .mockImplementation((url: string, init?: RequestInit) => {
        if (url === "/api/v1/sessions/Session-1")
          return Promise.resolve({
            ok: true,
            status: 200,
            json: async () => ({
              session: {
                name: "Session-1",
                spec: {
                  cameras: [{ name: "front", desiredState: "Present" }],
                  takes: [],
                },
                status: { cameras: [{ name: "front", phase: "Disconnected" }] },
              },
            }),
          });
        if (url === "/api/v1/livekit/token")
          return Promise.resolve({
            ok: true,
            status: 200,
            json: async () => ({
              serverUrl: "wss://livekit",
              roomName: "preview",
              participantToken: "token",
              expiresAt: "2026-07-14T01:05:00Z",
            }),
          });
        if (url.endsWith("/cameras/front") && init?.method === "DELETE")
          return Promise.resolve({
            ok: true,
            status: 202,
            json: async () => ({
              camera: { name: "front", phase: "Deleting" },
            }),
          });
        throw new Error(`unexpected URL ${url}`);
      });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    expect(
      await screen.findByText("Camera未接続: Disconnected"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cameraを削除" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/sessions/Session-1/cameras/front",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
    expect(screen.getByText("Status: Deleting")).toBeInTheDocument();
  });

  it("starts an all-camera take, reports exclusions, and stops it", async () => {
    window.history.replaceState({}, "", "/sessions/Session-1");
    const fetchMock = vi
      .fn()
      .mockImplementation((url: string, init?: RequestInit) => {
        if (url === "/api/v1/sessions/Session-1")
          return Promise.resolve({
            ok: true,
            status: 200,
            json: async () => ({
              session: {
                name: "Session-1",
                spec: {
                  cameras: [
                    { name: "front", desiredState: "Present" },
                    { name: "side", desiredState: "Present" },
                  ],
                  takes: [],
                },
                status: {
                  cameras: [
                    { name: "front", phase: "Connected" },
                    { name: "side", phase: "Disconnected" },
                  ],
                  takes: [],
                },
              },
            }),
          });
        if (url === "/api/v1/livekit/token")
          return Promise.resolve({
            ok: true,
            status: 200,
            json: async () => ({
              serverUrl: "wss://livekit",
              roomName: "preview",
              participantToken: "token",
              expiresAt: "2026-07-14T01:05:00Z",
            }),
          });
        if (url.endsWith("/takes") && init?.method === "POST")
          return Promise.resolve({
            ok: true,
            status: 202,
            json: async () => ({
              take: {
                name: "take-1",
                phase: "Pending",
                cameraNames: ["front"],
              },
              excludedCameras: [
                { name: "side", reason: "CAMERA_DISCONNECTED" },
              ],
            }),
          });
        if (url.endsWith("/takes/take-1/stop"))
          return Promise.resolve({
            ok: true,
            status: 202,
            json: async () => ({
              take: {
                name: "take-1",
                phase: "Stopping",
                cameraNames: ["front"],
              },
            }),
          });
        throw new Error(`unexpected URL ${url}`);
      });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    await screen.findByLabelText("Take名");
    fireEvent.change(screen.getByLabelText("Take名"), {
      target: { value: "take-1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Takeを開始" }));
    expect(
      await screen.findByText("除外Camera: side: CAMERA_DISCONNECTED"),
    ).toBeInTheDocument();
    expect(screen.getByText("Camera: front")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Takeを停止" }));
    expect(await screen.findByText(/take-1: Stopping/)).toBeInTheDocument();
  });
});
