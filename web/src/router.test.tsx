import { describe, expect, it } from "vitest";
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

describe("router", () => {
  it("registers the connections route", () => {
    expect(hasPath(router.routes as RouteNode[], "connections")).toBe(true);
  });
});
