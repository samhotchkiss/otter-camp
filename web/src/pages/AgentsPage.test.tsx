import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import AgentsPage from "./AgentsPage";

describe("AgentsPage", () => {
  it("renders figma baseline shell for permanent and chameleon agents", () => {
    render(<AgentsPage />);

    expect(screen.getByRole("heading", { name: "Agent Status" })).toBeInTheDocument();
    expect(screen.getByText("3 Permanent Cores • 4 Active Chameleons")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Logs" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Spawn Agent" })).toBeInTheDocument();

    expect(screen.getByText("Orchestrator")).toBeInTheDocument();
    expect(screen.getByText("Memory System")).toBeInTheDocument();

    expect(screen.getByRole("heading", { name: "Chameleon Agents (On-Demand)" })).toBeInTheDocument();
    expect(screen.getByText("Agent-127")).toBeInTheDocument();
    expect(screen.getByText("API Security Expert")).toBeInTheDocument();
    expect(screen.getByText(/Working on API Gateway/)).toBeInTheDocument();
  });
});
