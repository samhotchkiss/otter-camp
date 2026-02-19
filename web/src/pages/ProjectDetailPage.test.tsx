import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import ProjectDetailPage from "./ProjectDetailPage";

function renderProjectDetailPage(initialEntry = "/projects/project-2") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route path="/projects/:id" element={<ProjectDetailPage />} />
        <Route path="/issue/:issueId" element={<div data-testid="issue-detail-route" />} />
        <Route path="/review/:documentId" element={<div data-testid="content-review-route" />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ProjectDetailPage", () => {
  it("renders static figma baseline scaffold without api dependency", () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    renderProjectDetailPage();

    expect(screen.getByRole("heading", { level: 1, name: "API Gateway" })).toBeInTheDocument();
    expect(screen.getByText(/Core API gateway handling authentication/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /github\.com\/company\/api-gateway/i })).toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();

    fetchSpy.mockRestore();
  });

  it("renders figma-equivalent project stats and open issues list", () => {
    renderProjectDetailPage();

    expect(screen.getByText("Issues")).toBeInTheDocument();
    expect(screen.getByText("Branches")).toBeInTheDocument();
    expect(screen.getByText("Commits")).toBeInTheDocument();
    expect(screen.getByText("Contributors")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Open Issues" })).toBeInTheDocument();
    expect(screen.getByText("Fix API rate limiting")).toBeInTheDocument();
    expect(screen.getByText("approval needed")).toBeInTheDocument();
  });

  it("navigates to issue detail when an issue row is clicked", async () => {
    const user = userEvent.setup();
    renderProjectDetailPage();

    await user.click(screen.getByRole("link", { name: /Fix API rate limiting/i }));

    expect(screen.getByTestId("issue-detail-route")).toBeInTheDocument();
  });

  it("keeps activity and file explorer in right rail position", () => {
    renderProjectDetailPage();

    const rightRail = screen.getByTestId("project-detail-right-rail");
    expect(rightRail).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Recent Activity" })).toBeInTheDocument();
    expect(screen.getByTestId("project-detail-file-explorer")).toBeInTheDocument();
    expect(screen.getByText("README.md")).toBeInTheDocument();
  });

  it("routes markdown review links from the static file explorer", async () => {
    const user = userEvent.setup();
    renderProjectDetailPage();

    await user.click(screen.getByRole("link", { name: /README\.md/i }));

    expect(screen.getByTestId("content-review-route")).toBeInTheDocument();
  });
});
