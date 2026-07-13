import { render, screen } from "@testing-library/react";

import { App } from "./App";

describe("App", () => {
  it("renders the application title", () => {
    render(<App />);

    expect(
      screen.getByRole("heading", { name: "Kinugasa Recording" }),
    ).toBeInTheDocument();
  });
});
