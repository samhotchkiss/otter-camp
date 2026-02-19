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

  it("shared primitives define panel, card, chip, and status-dot contracts", () => {
    const primitivesCss = readFixture("./primitives.css");

    expect(primitivesCss).toContain(".oc-panel");
    expect(primitivesCss).toContain(".oc-card");
    expect(primitivesCss).toContain(".oc-chip");
    expect(primitivesCss).toContain(".oc-status-dot");
    expect(primitivesCss).toContain("var(--oc-color-surface-default)");
  });

  it("toolbar primitives provide reusable input/button patterns", () => {
    const primitivesCss = readFixture("./primitives.css");

    expect(primitivesCss).toContain(".oc-toolbar");
    expect(primitivesCss).toContain(".oc-toolbar-input");
    expect(primitivesCss).toContain(".oc-toolbar-button");
    expect(primitivesCss).toContain("var(--oc-shadow-focus)");
  });

  it("foundation documentation includes token and primitive migration guidance", () => {
    const foundationDocs = readFixture("./FOUNDATION.md");

    expect(foundationDocs).toContain("`--oc-*`");
    expect(foundationDocs).toContain("`oc-panel`");
    expect(foundationDocs).toContain("`oc-toolbar-button`");
    expect(foundationDocs).toContain("Do:");
    expect(foundationDocs).toContain("Don't:");
  });

  it("inbox redesign primitives define header, tabs, and row metadata hooks", () => {
    const indexCss = readFixture("../index.css");

    expect(indexCss).toContain(".inbox-header");
    expect(indexCss).toContain(".inbox-header-actions");
    expect(indexCss).toContain(".inbox-icon-action");
    expect(indexCss).toContain(".inbox-filter-tab.active");
    expect(indexCss).toContain(".inbox-list-container");
    expect(indexCss).toContain(".inbox-row");
    expect(indexCss).toContain(".inbox-row-meta");
    expect(indexCss).toContain("@media (max-width: 640px)");
  });

  it("shell layout primitives are defined for sidebar/header/workspace/chat", () => {
    const themeCss = readFixture("../theme.css");

    expect(themeCss).toContain(".shell-layout");
    expect(themeCss).toContain(".shell-sidebar");
    expect(themeCss).toContain(".shell-header");
    expect(themeCss).toContain(".shell-workspace");
    expect(themeCss).toContain(".shell-chat-slot");
    expect(themeCss).toContain("@media (max-width: 1024px)");
  });

  it("shell handoff notes exist for follow-on page redesign specs", () => {
    const shellGuide = readFixture("./SHELL_LAYOUT.md");

    expect(shellGuide).toContain("Spec 502+");
    expect(shellGuide).toContain("`shell-sidebar`");
    expect(shellGuide).toContain("`shell-chat-slot`");
    expect(shellGuide).toContain("Do:");
    expect(shellGuide).toContain("Don't:");
  });
});
