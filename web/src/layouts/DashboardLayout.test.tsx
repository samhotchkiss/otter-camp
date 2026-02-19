import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
  beforeEach(() => {
    window.localStorage.clear();
  });

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

  it("hydrates sidebar identity from the active session", async () => {
    window.localStorage.setItem("otter_camp_user", JSON.stringify({
      id: "user-1",
      name: "Sam Rivera",
      email: "sam@otter.camp",
    }));

    renderLayout("/projects");

    expect(await screen.findByText("Sam Rivera")).toBeInTheDocument();
    expect(screen.getByText("sam@otter.camp")).toBeInTheDocument();
    expect(screen.queryByText("Jane Smith")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sidebar user menu" })).toHaveTextContent("SR");
  });

  it("supports sidebar and chat panel toggles while preserving shell stability", async () => {
    renderLayout("/projects");

    const sidebar = await screen.findByTestId("shell-sidebar");
    const chatSlot = screen.getByTestId("shell-chat-slot");
    expect(chatSlot).toHaveAttribute("aria-hidden", "false");

    fireEvent.click(screen.getByRole("button", { name: "Toggle menu" }));
    expect(sidebar).toHaveClass("open");
    fireEvent.click(screen.getByRole("button", { name: "Close menu" }));
    expect(sidebar).not.toHaveClass("open");

    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).toHaveAttribute("aria-hidden", "true");
    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).toHaveAttribute("aria-hidden", "false");
  });

  it("marks the active projects navigation link with aria-current", async () => {
    renderLayout("/projects");
    const activeProjectsLink = await screen.findByRole("link", { name: "Projects" });
    expect(activeProjectsLink).toHaveAttribute("aria-current", "page");
  });

  it("applies responsive shell guard classes to prevent overflow drift", async () => {
    renderLayout("/projects");

    const sidebar = await screen.findByTestId("shell-sidebar");
    expect(sidebar).toHaveClass("fixed");
    expect(sidebar).toHaveClass("lg:relative");

    const menuToggle = screen.getByRole("button", { name: "Toggle menu" });
    expect(menuToggle).toHaveClass("md:hidden");

    const searchInput = screen.getByPlaceholderText("Search...");
    expect(searchInput.parentElement).toHaveClass("hidden");
    expect(searchInput.parentElement).toHaveClass("md:block");

    const workspace = screen.getByTestId("shell-workspace");
    expect(workspace).toHaveClass("overflow-hidden");

    const chatSlot = screen.getByTestId("shell-chat-slot");
    expect(chatSlot).toHaveClass("max-lg:hidden");

    const mainContent = document.getElementById("main-content");
    expect(mainContent).toHaveClass("min-w-0");
  });
});
