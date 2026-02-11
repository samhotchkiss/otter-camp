import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import AgentWelcome from "./AgentWelcome";

describe("AgentWelcome", () => {
  it("renders ready state and triggers actions", () => {
    const onStartChat = vi.fn();
    const onCreateAnother = vi.fn();

    render(
      <AgentWelcome
        name="Marcus"
        avatar="/assets/agent-profiles/marcus.webp"
        roleDescription="Chief of Staff"
        onStartChat={onStartChat}
        onCreateAnother={onCreateAnother}
      />,
    );

    expect(screen.getByText("Marcus is ready to go")).toBeInTheDocument();
    expect(screen.getByText("Chief of Staff")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Send First Message" }));
    expect(onStartChat).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "Create Another Agent" }));
    expect(onCreateAnother).toHaveBeenCalledTimes(1);
  });
});
