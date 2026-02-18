import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const readFixture = (relativePath: string) =>
  readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");

describe("design system stylesheet contract", () => {
  it("canonical token source is imported by theme.css", () => {
    const themeCss = readFixture("../theme.css");

    expect(themeCss).toContain("@import './styles/design-tokens.css';");
  });

  it("canonical token source defines semantic token groups", () => {
    const tokensCss = readFixture("./design-tokens.css");

    expect(tokensCss).toContain("--oc-color-bg-canvas");
    expect(tokensCss).toContain("--oc-color-surface-default");
    expect(tokensCss).toContain("--oc-color-text-primary");
    expect(tokensCss).toContain("--oc-font-sans");
    expect(tokensCss).toContain("--oc-space-md");
    expect(tokensCss).toContain("--oc-shadow-panel");
  });

  it("legacy token aliases keep existing variable contract", () => {
    const tokensCss = readFixture("./design-tokens.css");

    expect(tokensCss).toContain("--bg: var(--oc-color-bg-canvas);");
    expect(tokensCss).toContain("--surface: var(--oc-color-surface-default);");
    expect(tokensCss).toContain("--surface-alt: var(--oc-color-surface-muted);");
    expect(tokensCss).toContain("--text: var(--oc-color-text-primary);");
    expect(tokensCss).toContain("--text-muted: var(--oc-color-text-muted);");
    expect(tokensCss).toContain("--font: var(--oc-font-sans);");
    expect(tokensCss).toContain("--font-mono: var(--oc-font-mono);");
  });
});
