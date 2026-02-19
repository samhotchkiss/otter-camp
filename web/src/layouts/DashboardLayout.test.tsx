import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import DashboardLayout from "./DashboardLayout";
import { KeyboardShortcutsProvider } from "../contexts/KeyboardShortcutsContext";

vi.mock("../components/ShortcutsHelpModal", () => ({
  default: () => null,
}));

vi.mock("../components/DemoBanner", () => ({
  default: () => null,
}));

vi.mock("../components/GlobalSearch", () => ({
  default: () => null,
}));

vi.mock("../components/chat/GlobalChatDock", () => ({
  default: () => <div>chat-dock</div>,
}));

vi.mock("../hooks/useKeyboardShortcuts", () => ({
  useKeyboardShortcuts: () => undefined,
}));

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: () => ({ connected: true }),
}));

function renderLayout(pathname = "/projects") {
  return render(
    <MemoryRouter initialEntries={[pathname]}>
      <KeyboardShortcutsProvider>
        <DashboardLayout>
          <div>child</div>
        </DashboardLayout>
      </KeyboardShortcutsProvider>
    </MemoryRouter>,
  );
}

describe("DashboardLayout", () => {
  it("renders figma shell scaffold with sidebar sections and header controls", async () => {
    renderLayout("/projects");

    expect(await screen.findByTestId("shell-layout")).toBeInTheDocument();
    expect(screen.getByTestId("shell-sidebar")).toBeInTheDocument();
    expect(screen.getByTestId("shell-header")).toBeInTheDocument();
    expect(screen.getByTestId("shell-workspace")).toBeInTheDocument();
    expect(screen.getByTestId("shell-chat-slot")).toBeInTheDocument();
    expect(screen.getByText("Otter Camp")).toBeInTheDocument();
    expect(screen.getByText("Agent Ops")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Projects" })).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Search...")).toBeInTheDocument();
    expect(screen.getByTestId("shell-route-label")).toHaveTextContent("projects");
    expect(screen.getByText("child")).toBeInTheDocument();
  });

  it("shows the user menu and keeps settings links available", async () => {
    renderLayout("/connections");

    fireEvent.click(screen.getByRole("button", { name: "User menu" }));
    expect(await screen.findByRole("button", { name: "Agents" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Connections" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Feed" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Settings" })).toBeInTheDocument();
  });

  it("supports sidebar and chat panel toggles while preserving shell stability", async () => {
    renderLayout("/projects");

    const sidebar = await screen.findByTestId("shell-sidebar");
    const chatSlot = screen.getByTestId("shell-chat-slot");
    expect(chatSlot).not.toHaveClass("hidden");

    fireEvent.click(screen.getByRole("button", { name: "Toggle menu" }));
    expect(sidebar).toHaveClass("open");
    fireEvent.click(screen.getByRole("button", { name: "Close menu" }));
    expect(sidebar).not.toHaveClass("open");

    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).toHaveClass("hidden");
    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).not.toHaveClass("hidden");
  });

  it("marks the active projects navigation link with aria-current", async () => {
    renderLayout("/projects");
    const activeProjectsLink = await screen.findByRole("link", { name: "Projects" });
    expect(activeProjectsLink).toHaveAttribute("aria-current", "page");
  });
});
