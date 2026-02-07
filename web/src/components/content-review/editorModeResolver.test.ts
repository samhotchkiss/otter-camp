import { describe, expect, it } from "vitest";
import { resolveEditorComponent, resolveEditorForPath } from "./editorModeResolver";

describe("editor mode resolver", () => {
  it("maps known extensions to expected editor modes", () => {
    expect(resolveEditorForPath("/posts/2026-02-06-launch.md").editorMode).toBe("markdown");
    expect(resolveEditorForPath("/notes/scratch.txt").editorMode).toBe("text");
    expect(resolveEditorForPath("/notes/main.ts").editorMode).toBe("code");
    expect(resolveEditorForPath("/assets/mockup.PNG").editorMode).toBe("image");
  });

  it("maps unknown extensions to safe text fallback", () => {
    const resolution = resolveEditorForPath("/notes/spec.custom");
    expect(resolution.editorMode).toBe("text");
    expect(resolution.capabilities.supportsInlineComments).toBe(false);
    expect(resolution.capabilities.supportsSyntaxHighlight).toBe(false);
    expect(resolution.capabilities.supportsImagePreview).toBe(false);
  });

  it("drives editor component selection deterministically", () => {
    expect(resolveEditorComponent("/posts/2026-02-06-launch.md")).toBe("markdown_review");
    expect(resolveEditorComponent("/notes/scratch.txt")).toBe("plain_text");
    expect(resolveEditorComponent("/notes/server.go")).toBe("code_editor");
    expect(resolveEditorComponent("/assets/diagram.gif")).toBe("image_preview");
    expect(resolveEditorComponent("/notes/unknown.ext")).toBe("plain_text");
  });
});
