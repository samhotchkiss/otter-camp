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

  it("collapses chat rail to handle width and lets content expand when compacted", async () => {
    renderLayout("/projects");

    const chatSlot = await screen.findByTestId("shell-chat-slot");
    const mainContent = document.getElementById("main-content");
    const contentShell = mainContent?.firstElementChild as HTMLElement | null;
    expect(contentShell).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).toHaveAttribute("aria-hidden", "true");
    expect(chatSlot).toHaveStyle({ width: "16px" });
    expect(contentShell).toHaveClass("w-full");
    expect(contentShell).toHaveClass("max-w-none");

    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).toHaveAttribute("aria-hidden", "false");
    expect(contentShell).toHaveClass("mx-auto");
    expect(contentShell).toHaveClass("max-w-6xl");
  });

  it("restores chat rail width from persisted localStorage value on load", async () => {
    window.localStorage.setItem("otter-shell-width", "444");

    renderLayout("/projects");

    const chatSlot = await screen.findByTestId("shell-chat-slot");
    expect(chatSlot).toHaveStyle({ width: "444px" });
  });

  it("falls back to default chat rail width when persisted value is missing or invalid", async () => {
    renderLayout("/projects");

    const chatSlot = await screen.findByTestId("shell-chat-slot");
    expect(chatSlot).toHaveStyle({ width: "384px" });
  });

  it("clamps restored chat rail width to viewport-aware min/max bounds", async () => {
    const originalInnerWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 900,
      writable: true,
    });
    window.localStorage.setItem("otter-shell-width", "2000");

    renderLayout("/projects");

    const chatSlot = await screen.findByTestId("shell-chat-slot");
    expect(chatSlot).toHaveStyle({ width: "680px" });

    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: originalInnerWidth,
      writable: true,
    });
  });

  it("persists resized chat rail width after drag interactions", async () => {
    renderLayout("/projects");

    const chatSlot = await screen.findByTestId("shell-chat-slot");
    const resizeHandle = screen.getByRole("button", { name: "Slide chat closed" });

    fireEvent.mouseDown(resizeHandle, { clientX: 900 });
    fireEvent.mouseMove(window, { clientX: 800 });
    fireEvent.mouseUp(window);

    expect(chatSlot).toHaveStyle({ width: "484px" });
    expect(window.localStorage.getItem("otter-shell-width")).toBe("484");
  });

  it("persists clamped width values when drag exceeds min and max bounds", async () => {
    const originalInnerWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 1024,
      writable: true,
    });

    renderLayout("/projects");
    const chatSlot = await screen.findByTestId("shell-chat-slot");
    const resizeHandle = screen.getByRole("button", { name: "Slide chat closed" });

    fireEvent.mouseDown(resizeHandle, { clientX: 900 });
    fireEvent.mouseMove(window, { clientX: 2000 });
    fireEvent.mouseUp(window);
    expect(chatSlot).toHaveStyle({ width: "320px" });
    expect(window.localStorage.getItem("otter-shell-width")).toBe("320");

    fireEvent.mouseDown(resizeHandle, { clientX: 900 });
    fireEvent.mouseMove(window, { clientX: 0 });
    fireEvent.mouseUp(window);
    expect(chatSlot).toHaveStyle({ width: "804px" });
    expect(window.localStorage.getItem("otter-shell-width")).toBe("804");

    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: originalInnerWidth,
      writable: true,
    });
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
