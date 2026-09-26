import type { PreviewAccess } from "../api/types";

/** Builds the authenticated WebTransport URL without mutating the API value. */
export function createMoqConnectionUrl(access: PreviewAccess): URL {
  const url = new URL(access.url);
  if (url.protocol !== "https:") {
    throw new Error("Media over QUIC endpoint must use HTTPS");
  }
  url.searchParams.set("jwt", access.accessToken);
  return url;
}
