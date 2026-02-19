import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import IssueDetailPage from "./IssueDetailPage";

function renderProjectIssueRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/projects/:id/issues/:issueId" element={<IssueDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

function renderAliasIssueRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/issue/:issueId" element={<IssueDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("IssueDetailPage", () => {
  it("renders figma-baseline issue detail structure for project issue route", () => {
    renderProjectIssueRoute("/projects/project-2/issues/issue-209");

    expect(screen.getByTestId("issue-detail-shell")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Fix API rate limiting" })).toBeInTheDocument();
    expect(screen.getByText("ISS-209")).toBeInTheDocument();
    expect(screen.getByText("Proposed Solution Awaiting Approval")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Approve Solution/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Request Changes/ })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Discussion" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Timeline" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Related Issues" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Details" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Review Documentation/ })).toHaveAttribute("href", "/review/rate-limiting-docs");
  });

  it("renders the same baseline shell for alias issue route", () => {
    renderAliasIssueRoute("/issue/issue-209");

    expect(screen.getByTestId("issue-detail-shell")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Fix API rate limiting" })).toBeInTheDocument();
    expect(screen.getByText("API Gateway")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Approve Solution/ })).toBeInTheDocument();
  });
});
