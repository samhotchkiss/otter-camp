import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AgentProfile } from "../../data/agent-profiles";
import AgentCustomizer from "./AgentCustomizer";

const profile: AgentProfile = {
  id: "rory",
  name: "Rory",
  tagline: "Precise and opinionated.",
  roleCategory: "Engineering",
  roleDescription: "Code Reviewer",
  personalityPreview: "Catches edge cases and demands clarity.",
  defaultModel: "gpt-5.2-codex",
  defaultAvatar: "/assets/agent-profiles/rory.webp",
  defaultSoul: "# SOUL\n{{name}} values correctness.",
  defaultIdentity: "# IDENTITY\n- Name: {{name}}",
  searchableText: "engineering code reviewer precise",
  isStarter: true,
};

describe("AgentCustomizer", () => {
  it("prefills profile defaults and emits submit payload", () => {
    const onSubmit = vi.fn();
    render(<AgentCustomizer profile={profile} onSubmit={onSubmit} onBack={vi.fn()} submitting={false} />);

    expect(screen.getByLabelText("Name")).toHaveValue("Rory");
    expect(screen.getByLabelText("Model")).toHaveValue("gpt-5.2-codex");

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Rory Prime" } });
    fireEvent.change(screen.getByLabelText("SOUL.md"), { target: { value: "# SOUL\nCustom" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        displayName: "Rory Prime",
        profileId: "rory",
        soul: "# SOUL\nCustom",
      }),
    );
  });

  it("calls onBack from back button", () => {
    const onBack = vi.fn();
    render(<AgentCustomizer profile={profile} onSubmit={vi.fn()} onBack={onBack} submitting={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });
});
