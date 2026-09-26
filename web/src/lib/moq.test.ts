import { describe, expect, it } from "vitest";
import { createMoqConnectionUrl } from "./moq";

describe("createMoqConnectionUrl", () => {
  it("adds the opaque access token as the MoQ jwt query parameter", () => {
    const url = createMoqConnectionUrl({
      url: "https://media.example.com/moq/session-id?region=local",
      accessToken: "opaque/token?value",
      expiresAt: "2026-09-26T12:00:00Z",
    });

    expect(url.origin).toBe("https://media.example.com");
    expect(url.pathname).toBe("/moq/session-id");
    expect(url.searchParams.get("region")).toBe("local");
    expect(url.searchParams.get("jwt")).toBe("opaque/token?value");
  });

  it("rejects an endpoint that cannot be used by WebTransport", () => {
    expect(() => createMoqConnectionUrl({
      url: "http://media.example.com/moq/session-id",
      accessToken: "opaque-token",
      expiresAt: "2026-09-26T12:00:00Z",
    })).toThrow("must use HTTPS");
  });
});
