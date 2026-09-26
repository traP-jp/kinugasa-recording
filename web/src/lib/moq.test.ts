import { describe, expect, it } from "vitest";
import { createMoqConnectionUrl, createMoqServerCertificateHashes } from "./moq";

describe("createMoqConnectionUrl", () => {
  it("adds the opaque access token as the MoQ jwt query parameter", () => {
    const url = createMoqConnectionUrl({
      url: "https://media.example.com/moq/session-id?region=local",
      accessToken: "opaque/token?value",
      expiresAt: "2026-09-26T12:00:00Z",
      serverCertificateHashes: [],
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
      serverCertificateHashes: [],
    })).toThrow("must use HTTPS");
  });
});

describe("createMoqServerCertificateHashes", () => {
  it("creates SHA-256 WebTransport certificate hashes", () => {
    const value = "0123456789abcdef".repeat(4);

    expect(createMoqServerCertificateHashes({
      url: "https://media.example.com/moq/session-id",
      accessToken: "opaque-token",
      expiresAt: "2026-09-26T12:00:00Z",
      serverCertificateHashes: [value],
    })).toEqual([{ algorithm: "sha-256", value }]);
  });

  it("rejects malformed certificate hashes", () => {
    expect(() => createMoqServerCertificateHashes({
      url: "https://media.example.com/moq/session-id",
      accessToken: "opaque-token",
      expiresAt: "2026-09-26T12:00:00Z",
      serverCertificateHashes: ["not-a-sha256-hash"],
    })).toThrow("must be a SHA-256 hex digest");
  });
});
