import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import MessageAvatar from "../MessageAvatar";

describe("MessageAvatar", () => {
  it("uses initials for agent fallback avatars", () => {
    render(<MessageAvatar name="Jeff G" senderType="agent" />);

    const avatar = screen.getByLabelText("Agent avatar");
    expect(avatar).toHaveTextContent("JG");
    expect(avatar).not.toHaveTextContent("🤖");
  });

  it("uses initials for user fallback avatars", () => {
    render(<MessageAvatar name="Sam Hotchkiss" senderType="user" />);
    expect(screen.getByLabelText("User avatar")).toHaveTextContent("SH");
  });
});
