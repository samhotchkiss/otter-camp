import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import DocumentWorkspace from "./DocumentWorkspace";

describe("DocumentWorkspace mode routing", () => {
  it("loads the expected editor mode per file extension", () => {
    const { rerender } = render(
      <DocumentWorkspace path="/posts/2026-02-06-launch.md" content="# Draft" reviewerName="Sam" />
    );
    expect(screen.getByTestId("editor-mode-markdown")).toBeInTheDocument();

    rerender(<DocumentWorkspace path="/notes/todo.txt" content="todo" reviewerName="Sam" />);
    expect(screen.getByTestId("editor-mode-text")).toBeInTheDocument();

    rerender(
      <DocumentWorkspace
        path="/notes/main.ts"
        content={"export const value = 2;\n"}
        previousContent={"export const value = 1;\n"}
        reviewerName="Sam"
      />
    );
    expect(screen.getByTestId("editor-mode-code")).toBeInTheDocument();

    rerender(
      <DocumentWorkspace
        path="/assets/cover.png"
        content=""
        imageSrc="https://example.com/cover.png"
        reviewerName="Sam"
      />
    );
    expect(screen.getByTestId("editor-mode-image")).toBeInTheDocument();
  });

  it("renders syntax-highlight preview and diff view in code mode", () => {
    render(
      <DocumentWorkspace
        path="/notes/app.go"
        content={"package main\n\nfunc main() { println(\"v2\") }\n"}
        previousContent={"package main\n\nfunc main() { println(\"v1\") }\n"}
        reviewerName="Sam"
      />
    );

    expect(screen.getByTestId("code-syntax-preview")).toBeInTheDocument();
    expect(screen.getByTestId("code-diff-view")).toBeInTheDocument();
    expect(screen.getByTestId("code-diff-view")).toHaveTextContent("println");
  });

  it("shows graceful fallback when image preview fails", () => {
    render(
      <DocumentWorkspace
        path="/assets/mockup.jpg"
        content=""
        imageSrc="https://example.com/missing.jpg"
        reviewerName="Sam"
      />
    );

    const image = screen.getByTestId("image-preview");
    fireEvent.error(image);

    expect(screen.getByTestId("image-fallback")).toBeInTheDocument();
  });

  it("does not leak stale editor state when switching files across modes", async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <DocumentWorkspace path="/notes/draft.txt" content="alpha" reviewerName="Sam" />
    );

    const textEditor = screen.getByTestId("text-editor") as HTMLTextAreaElement;
    await user.clear(textEditor);
    await user.type(textEditor, "changed");
    expect(textEditor.value).toBe("changed");

    rerender(
      <DocumentWorkspace
        path="/notes/main.py"
        content={"print('fresh')\n"}
        previousContent=""
        reviewerName="Sam"
      />
    );
    expect((screen.getByTestId("code-editor-input") as HTMLTextAreaElement).value).toContain("fresh");

    rerender(<DocumentWorkspace path="/notes/draft.txt" content="beta" reviewerName="Sam" />);
    expect((screen.getByTestId("text-editor") as HTMLTextAreaElement).value).toBe("beta");
  });
});
