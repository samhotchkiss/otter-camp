import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import AgentSelector from "../AgentSelector";
import type { Agent } from "../types";

const agents: Agent[] = [
  { id: "agent-1", name: "Alice", status: "online", role: "Planner" },
  { id: "agent-2", name: "Bob", status: "offline", role: "Developer" },
];

describe("AgentSelector", () => {
  it("filters agents by search query", async () => {
    const user = userEvent.setup();
    render(<AgentSelector agents={agents} />);

    const input = screen.getByLabelText(/Search agents/i);
    await user.type(input, "bob");

    expect(
      screen.queryByRole("button", { name: /Alice/i }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Bob/i })).toBeInTheDocument();
  });

  it("calls onSelect when an agent is clicked", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<AgentSelector agents={agents} onSelect={onSelect} />);

    await user.click(screen.getByRole("button", { name: /Alice/i }));

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(agents[0]);
  });

  it("shows an empty state when no agents match", async () => {
    const user = userEvent.setup();
    render(<AgentSelector agents={agents} />);

    const input = screen.getByLabelText(/Search agents/i);
    await user.type(input, "zzz");

    expect(screen.getByText(/No agents found/i)).toBeInTheDocument();
  });
});

