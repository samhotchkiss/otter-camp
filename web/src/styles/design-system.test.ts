import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const readFixture = (relativePath: string) =>
  readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");

describe("design system stylesheet contract", () => {
  it("shell layout primitives are defined for sidebar/header/workspace/chat", () => {
    const themeCss = readFixture("../theme.css");

    expect(themeCss).toContain(".shell-layout");
    expect(themeCss).toContain(".shell-sidebar");
    expect(themeCss).toContain(".shell-header");
    expect(themeCss).toContain(".shell-workspace");
    expect(themeCss).toContain(".shell-chat-slot");
    expect(themeCss).toContain("@media (max-width: 1024px)");
  });
});
