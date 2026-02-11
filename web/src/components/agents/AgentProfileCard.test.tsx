import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AgentProfile } from "../../data/agent-profiles";
import AgentProfileCard from "./AgentProfileCard";

const profile: AgentProfile = {
  id: "marcus",
  name: "Marcus",
  tagline: "Calm, organized, sees the big picture.",
  roleCategory: "Operations",
  roleDescription: "Chief of Staff",
  personalityPreview: "Reads every thread before deciding. Communicates clearly and in order.",
  defaultModel: "claude-opus-4-6",
  defaultAvatar: "/assets/agent-profiles/marcus.webp",
  defaultSoul: "# SOUL\n{{name}} keeps the team aligned.",
  defaultIdentity: "# IDENTITY\n- Name: {{name}}",
  searchableText: "operations chief of staff calm organized",
  isStarter: true,
};

describe("AgentProfileCard", () => {
  it("renders profile data and triggers selection", () => {
    const onSelect = vi.fn();
    render(<AgentProfileCard profile={profile} isSelected={false} onSelect={onSelect} />);

    expect(screen.getByText("Marcus")).toBeInTheDocument();
    expect(screen.getByText("Chief of Staff")).toBeInTheDocument();
    expect(screen.getByText("Operations")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Marcus/i }));
    expect(onSelect).toHaveBeenCalledWith("marcus");
  });

  it("marks selected state", () => {
    render(<AgentProfileCard profile={profile} isSelected onSelect={vi.fn()} />);
    expect(screen.getByRole("button", { name: /Marcus/i })).toHaveAttribute("aria-pressed", "true");
  });
});
