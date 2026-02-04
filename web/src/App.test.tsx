import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import App from "./App";

describe("App", () => {
  it("renders app without crashing", () => {
    const { container } = render(<App />);
    expect(container).toBeTruthy();
  });

  it("shows Otter Camp heading", () => {
    render(<App />);
    expect(screen.getByText(/Otter Camp/i)).toBeTruthy();
  });
});
