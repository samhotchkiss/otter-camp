import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import ConnectionsPage from "./ConnectionsPage";

describe("ConnectionsPage", () => {
  it("renders figma ops baseline sections", () => {
    render(<ConnectionsPage />);

    expect(screen.getByRole("heading", { name: "Operations" })).toBeInTheDocument();
    expect(screen.getByText("CLI • Integrations • Compliance")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Multi-Tenant" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "System Healthy" })).toBeInTheDocument();

    expect(screen.getByRole("heading", { name: "OpenClaw Bridge" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Job Scheduler" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Compliance Reviews" })).toBeInTheDocument();

    expect(screen.getByText("otter init")).toBeInTheDocument();
    expect(screen.getByText("GitHub Sync")).toBeInTheDocument();
  });
});
