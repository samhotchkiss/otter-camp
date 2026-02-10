import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import App from "./App";
import { router } from "./router";

type RouteNode = {
  path?: string;
  children?: RouteNode[];
};

function hasPath(routes: RouteNode[], targetPath: string): boolean {
  for (const route of routes) {
    if (route.path === targetPath) {
      return true;
    }
    if (Array.isArray(route.children) && hasPath(route.children, targetPath)) {
      return true;
    }
  }
  return false;
}

describe("App", () => {
  it("renders app without crashing", () => {
    const { container } = render(<App />);
    expect(container).toBeTruthy();
  });

  it("shows login branding", () => {
    render(<App />);
    expect(screen.getByText(/otter\.camp/i)).toBeTruthy();
  });

  it("registers memory evaluation dashboard route", () => {
    expect(hasPath(router.routes as RouteNode[], "knowledge/evaluation")).toBe(true);
  });
});
