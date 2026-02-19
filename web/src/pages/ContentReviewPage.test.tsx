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
    </MemoryRouter>,
  );
}

describe("ContentReviewPage", () => {
  it("renders figma-baseline review layout and controls", () => {
    renderRoute("/review/docs%2Frate-limiting-implementation.md");

    expect(screen.getByTestId("content-review-page-shell")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Content Review" })).toBeInTheDocument();
    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("docs/rate-limiting-implementation.md");
    expect(screen.getByRole("button", { name: /Request Changes/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Approve/ })).toBeInTheDocument();
    expect(screen.getByText("Comments")).toBeInTheDocument();
    expect(screen.getByText("Unresolved")).toBeInTheDocument();
    expect(screen.getByText("Resolved")).toBeInTheDocument();
    expect(screen.getByText("Document")).toBeInTheDocument();
    expect(screen.getByText("All Comments")).toBeInTheDocument();
  });

  it("keeps malformed route segments stable without crashing", () => {
    renderRoute("/review/%E0%A4%A.md");
    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("%E0%A4%A.md");
  });

  it("falls back to baseline default path when route segment is empty after trim", () => {
    renderRoute("/review/%20");
    expect(screen.getByTestId("content-review-route-path")).toHaveTextContent("docs/rate-limiting-implementation.md");
  });
});
