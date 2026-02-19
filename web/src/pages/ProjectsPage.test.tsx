import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import ProjectsPage from "./ProjectsPage";

function renderProjectsPage() {
  return render(
    <MemoryRouter initialEntries={["/projects"]}>
      <Routes>
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/projects/:id" element={<div data-testid="project-detail-route" />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ProjectsPage", () => {
  it("renders static figma baseline scaffold without api dependency", () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    renderProjectsPage();

    expect(screen.getByRole("heading", { name: "Projects" })).toBeInTheDocument();
    expect(screen.getByText("Git-backed repositories & tracking")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New Project" })).toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();

    fetchSpy.mockRestore();
  });

  it("renders figma-equivalent project cards and metric chips", () => {
    renderProjectsPage();

    const customerPortalCard = screen.getByTestId("project-card-project-1");
    expect(customerPortalCard).toBeInTheDocument();
    expect(screen.getByText("Customer Portal")).toBeInTheDocument();
    expect(screen.getByText("ottercamp/customer-portal")).toBeInTheDocument();
    expect(screen.getAllByText("Open")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Active")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Review")[0]).toBeInTheDocument();
    expect(screen.getAllByText("Synced").length).toBeGreaterThan(0);
    expect(screen.getByText("Local")).toBeInTheDocument();
  });

  it("navigates to project detail route when a project card is clicked", async () => {
    const user = userEvent.setup();
    renderProjectsPage();

    await user.click(screen.getByRole("link", { name: /Customer Portal/i }));

    expect(screen.getByTestId("project-detail-route")).toBeInTheDocument();
  });

  it("renders figma-equivalent recent activity list", () => {
    renderProjectsPage();

    expect(screen.getByRole("heading", { name: "Recent Activity" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View all recent activity" })).toBeInTheDocument();
    expect(screen.getByText("Add user authentication flow")).toBeInTheDocument();
    expect(screen.getByText("Fix API rate limiting")).toBeInTheDocument();
    expect(screen.getAllByText("needs approval").length).toBeGreaterThan(0);
  });
});
