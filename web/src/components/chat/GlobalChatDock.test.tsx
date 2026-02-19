import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import GlobalChatDock from "./GlobalChatDock";
import type { GlobalChatConversation } from "../../contexts/GlobalChatContext";

const globalChatState = {
  isOpen: false,
  totalUnread: 0,
  agentNamesByID: new Map<string, string>(),
  resolveAgentName: (raw: string) => raw,
  conversations: [] as GlobalChatConversation[],
  selectedConversation: null as GlobalChatConversation | null,
  selectedKey: null as string | null,
  setDockOpen: vi.fn(),
  toggleDock: vi.fn(),
  selectConversation: vi.fn(),
  markConversationRead: vi.fn(),
  removeConversation: vi.fn(),
  archiveConversation: vi.fn(async () => true),
};

vi.mock("../../contexts/GlobalChatContext", () => ({
  useGlobalChat: () => globalChatState,
}));

describe("GlobalChatDock", () => {
  beforeEach(() => {
    globalChatState.isOpen = false;
    globalChatState.totalUnread = 0;
    globalChatState.selectedConversation = null;
    globalChatState.selectedKey = null;
    globalChatState.setDockOpen.mockReset();
    globalChatState.toggleDock.mockReset();
  });

  it("renders a collapsed launcher when closed", async () => {
    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    expect(screen.getByRole("button", { name: "Open global chat" })).toBeInTheDocument();
    expect(screen.getByText("Chat")).toBeInTheDocument();
  });

  it("opens dock when launcher is clicked", async () => {
    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Open global chat" }));
    expect(globalChatState.setDockOpen).toHaveBeenCalledWith(true);
  });

  it("renders figma baseline header and collapse control when open", async () => {
    globalChatState.isOpen = true;

    render(
      <MemoryRouter initialEntries={["/inbox"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    expect(screen.getByRole("heading", { name: "Global Chat" })).toBeInTheDocument();
    expect(screen.getByTestId("global-chat-context-cue")).toHaveTextContent("Main context");
    expect(screen.getByRole("button", { name: "Collapse global chat" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Fullscreen chat" })).toBeInTheDocument();
  });

  it("uses route context when open", async () => {
    globalChatState.isOpen = true;

    render(
      <MemoryRouter initialEntries={["/projects/project-1/issues/issue-1"]}>
        <GlobalChatDock />
      </MemoryRouter>,
    );

    expect(screen.getByTestId("global-chat-context-cue")).toHaveTextContent("Issue context");
    expect(screen.getByText("ISS-209")).toBeInTheDocument();
  });
});
