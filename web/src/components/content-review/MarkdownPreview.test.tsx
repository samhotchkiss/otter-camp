import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import MarkdownPreview from "./MarkdownPreview";

describe("MarkdownPreview CriticMarkup rendering", () => {
  it("renders author-attributed CriticMarkup comment bubbles", () => {
    render(
      <MarkdownPreview markdown={"Hello {>>AB: tighten this intro<<} world"} />
    );

    expect(screen.getByTestId("critic-comment-bubble")).toBeInTheDocument();
    expect(screen.getByTestId("critic-comment-author")).toHaveTextContent("AB");
    expect(screen.getByText("tighten this intro")).toBeInTheDocument();
    expect(screen.queryByText("{>>AB: tighten this intro<<}")).not.toBeInTheDocument();
  });

  it("renders legacy CriticMarkup comments without author badge", () => {
    render(<MarkdownPreview markdown={"Hello {>> tighten this intro <<} world"} />);

    expect(screen.getByTestId("critic-comment-bubble")).toBeInTheDocument();
    expect(screen.queryByTestId("critic-comment-author")).not.toBeInTheDocument();
    expect(screen.getByText("tighten this intro")).toBeInTheDocument();
  });
});
