import { describe, expect, it } from "vitest";
import { previewGridColumnCount } from "./previewGrid";

describe("previewGridColumnCount", () => {
  it.each([
    [0, 1],
    [1, 1],
    [2, 2],
    [4, 2],
    [5, 3],
    [9, 3],
    [10, 4],
    [16, 4],
    [17, 5],
  ])("maps %i items to %i columns", (itemCount, expected) => {
    expect(previewGridColumnCount(itemCount)).toBe(expected);
  });
});
