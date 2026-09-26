import type { PreviewAccess } from "../api/types";

export interface MoqServerCertificateHash {
  algorithm: "sha-256";
  value: string;
}

/** Builds the authenticated WebTransport URL without mutating the API value. */
export function createMoqConnectionUrl(access: PreviewAccess): URL {
  const url = new URL(access.url);
  if (url.protocol !== "https:") {
    throw new Error("Media over QUIC endpoint must use HTTPS");
  }
  url.searchParams.set("jwt", access.accessToken);
  return url;
}

/** Converts API certificate fingerprints to the shape expected by WebTransport. */
export function createMoqServerCertificateHashes(
  access: PreviewAccess,
): MoqServerCertificateHash[] {
  return access.serverCertificateHashes.map((value) => {
    if (!/^[0-9a-f]{64}$/i.test(value)) {
      throw new Error("MoQ server certificate hash must be a SHA-256 hex digest");
    }
    return { algorithm: "sha-256", value };
  });
}
