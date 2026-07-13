import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, vi } from "vitest";

import { App } from "./App";

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
});
