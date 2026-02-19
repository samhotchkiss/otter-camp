import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import ContentReviewPage from "./ContentReviewPage";

function renderRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/review/:documentId" element={<ContentReviewPage />} />
      </Routes>
    </MemoryRouter>
  );
}

describe("ContentReviewPage", () => {
  it("renders the redesigned route shell and decodes alias paths", () => {
    renderRoute("/review/docs%2Fguides%2Fapi%20spec.md");

    expect(screen.getByTestId("content-review-page-shell")).toBeInTheDocument();
    expect(screen.getByTestId("content-review-page-shell")).toHaveClass("min-w-0");
    expect(screen.getByTestId("content-review-route-header")).toBeInTheDocument();
    expect(screen.getByTestId("content-review-route-header")).toHaveClass("flex-col");
    expect(screen.getByTestId("content-review-route-header")).toHaveClass("sm:flex-row");
    expect(screen.getByRole("heading", { name: "Content Review" })).toBeInTheDocument();
    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("docs/guides/api spec.md");
    expect(screen.getByTestId("content-review-shell")).toBeInTheDocument();

    const sourceTextarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    expect(sourceTextarea.value).toContain("# Review: docs/guides/api spec.md");
  });

  it("falls back to untitled path when alias route document is empty after trim", () => {
    renderRoute("/review/%20");

    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("untitled.md");
    const sourceTextarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    expect(sourceTextarea.value).toContain("# Review: untitled.md");
  });

  it("keeps malformed encoded route segments stable without crashing", () => {
    renderRoute("/review/%E0%A4%A.md");

    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("%E0%A4%A.md");
    const sourceTextarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    expect(sourceTextarea.value).toContain("# Review: %E0%A4%A.md");
  });
});
