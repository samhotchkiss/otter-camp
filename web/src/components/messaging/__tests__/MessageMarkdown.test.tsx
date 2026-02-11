import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import MessageMarkdown, { resolveOtterInternalLink } from "../MessageMarkdown";

describe("resolveOtterInternalLink", () => {
  it("classifies project issue routes as internal", () => {
    expect(resolveOtterInternalLink("/projects/project-1/issues/issue-2")).toMatchObject({
      href: "/projects/project-1/issues/issue-2",
      kind: "Issue",
      detail: "Project project-1 · Issue issue-2",
    });
  });

  it("ignores API links", () => {
    expect(resolveOtterInternalLink("/api/projects/project-1")).toBeNull();
  });

  it("ignores external hosts", () => {
    expect(resolveOtterInternalLink("https://example.com/projects/project-1")).toBeNull();
  });
});

describe("MessageMarkdown", () => {
  it("renders OtterCamp links as context cards", () => {
    render(
      <MessageMarkdown markdown="[Open issue](https://localhost:4200/projects/project-1/issues/issue-2)" />,
    );

    const card = screen.getByTestId("otter-internal-link-card");
    expect(card).toBeInTheDocument();
    expect(card).toHaveAttribute("href", "/projects/project-1/issues/issue-2");
    expect(screen.getByText("Issue")).toBeInTheDocument();
    expect(screen.getByText("Open issue")).toBeInTheDocument();
    expect(screen.getByText("Project project-1 · Issue issue-2")).toBeInTheDocument();
  });

  it("keeps external links as regular anchors", () => {
    render(<MessageMarkdown markdown="[Open docs](https://example.com/docs)" />);

    const link = screen.getByRole("link", { name: "Open docs" });
    expect(link).toHaveAttribute("href", "https://example.com/docs");
    expect(link).toHaveAttribute("target", "_blank");
    expect(screen.queryByTestId("otter-internal-link-card")).not.toBeInTheDocument();
  });
});
