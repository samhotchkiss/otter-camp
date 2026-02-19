import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import GlobalChatSurface from "./GlobalChatSurface";
import type {
  GlobalChatConversation,
  GlobalDMConversation,
  GlobalIssueConversation,
  GlobalProjectConversation,
} from "../../contexts/GlobalChatContext";

function renderSurface(conversation: GlobalChatConversation | null) {
  return render(<GlobalChatSurface conversation={conversation} />);
}

describe("GlobalChatSurface", () => {
  it("renders main-context baseline by default", async () => {
    renderSurface(null);

    expect(screen.getByText("Main context")).toBeInTheDocument();
    expect(screen.getByText("Otter Shell")).toBeInTheDocument();
    expect(screen.getByText(/systems are online/i)).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Type a command or ask Frank...")).toBeInTheDocument();
  });

  it("renders DM context cues and placeholder", async () => {
    const dmConversation: GlobalDMConversation = {
      key: "dm:dm_marcus",
      type: "dm",
      threadId: "dm_marcus",
      title: "Marcus",
      contextLabel: "Direct message",
      subtitle: "Agent chat",
      unreadCount: 0,
      updatedAt: "2026-02-08T00:00:00.000Z",
      agent: {
        id: "agent-marcus",
        name: "Marcus",
        status: "online",
      },
    };

    renderSurface(dmConversation);

    expect(screen.getByText("DM context")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Message Marcus...")).toBeInTheDocument();
  });

  it("renders project and issue context placeholders", async () => {
    const projectConversation: GlobalProjectConversation = {
      key: "project:proj-1",
      type: "project",
      projectId: "proj-1",
      title: "API Gateway",
      contextLabel: "Project",
      subtitle: "Team thread",
      unreadCount: 0,
      updatedAt: "2026-02-08T00:00:00.000Z",
    };
    const issueConversation: GlobalIssueConversation = {
      key: "issue:iss-1",
      type: "issue",
      issueId: "iss-1",
      projectId: "proj-1",
      title: "ISS-209",
      contextLabel: "Issue",
      subtitle: "Issue thread",
      unreadCount: 0,
      updatedAt: "2026-02-08T00:00:00.000Z",
    };

    const { rerender } = render(<GlobalChatSurface conversation={projectConversation} />);
    expect(screen.getByText("Project context")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Ask about API Gateway...")).toBeInTheDocument();

    rerender(<GlobalChatSurface conversation={issueConversation} />);
    expect(screen.getByText("Issue context")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Discuss ISS-209...")).toBeInTheDocument();
  });

  it("sends a message and appends a simulated assistant reply", async () => {
    const user = userEvent.setup();

    renderSurface(null);

    const composer = screen.getByPlaceholderText("Type a command or ask Frank...");
    await user.type(composer, "Ship it");
    await user.click(screen.getByRole("button", { name: "Send message" }));

    expect(screen.getByText("Ship it")).toBeInTheDocument();

    expect(await screen.findByText(/analyzing request/i)).toBeInTheDocument();
  });
});
