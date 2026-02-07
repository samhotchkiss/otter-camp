import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import AgentStatusIndicator from "../AgentStatusIndicator";

describe("AgentStatusIndicator", () => {
  it("renders a status role and label", () => {
    render(<AgentStatusIndicator status="online" />);
    expect(screen.getByRole("status", { name: "Online" })).toBeInTheDocument();
  });

  it("renders a visible label when showLabel is true", () => {
    render(<AgentStatusIndicator status="offline" showLabel />);
    expect(screen.getByText("Offline")).toBeInTheDocument();
  });
});

