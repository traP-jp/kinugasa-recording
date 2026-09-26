import { describe, expect, it } from "vitest";
import { buildMoblinUrl } from "./moblin";

describe("buildMoblinUrl", () => {
  it("encodes a selected H.264 30 fps RIST stream", () => {
    const url = buildMoblinUrl(
      "session-1",
      "camera-a",
      "rist://camera.example.com:9000?aes-type=256&secret=a+b/c=",
    );

    expect(url.startsWith("moblin://?")).toBe(true);
    expect(JSON.parse(decodeURIComponent(url.slice("moblin://?".length)))).toEqual({
      streams: [{
        name: "session-1_camera-a",
        url: "rist://camera.example.com:9000?aes-type=256&secret=a+b/c=",
        selected: true,
        video: {
          codec: "H.264/AVC",
          fps: 30,
        },
      }],
    });
  });

  it("preserves characters that must be encoded in a custom URL query", () => {
    const url = buildMoblinUrl(
      "session 日本語",
      "camera 日本語",
      "rist://example.com:9000?secret=a&b=c",
    );
    const settings = JSON.parse(decodeURIComponent(url.slice("moblin://?".length)));

    expect(settings.streams[0].name).toBe("session 日本語_camera 日本語");
    expect(settings.streams[0].url).toBe("rist://example.com:9000?secret=a&b=c");
  });
});
