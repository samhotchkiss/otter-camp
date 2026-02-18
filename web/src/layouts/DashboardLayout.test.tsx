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
  default: () => null,
}));

vi.mock("../hooks/useKeyboardShortcuts", () => ({
  useKeyboardShortcuts: () => undefined,
}));

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: () => ({ connected: true }),
}));

const { inboxMock } = vi.hoisted(() => ({
  inboxMock: vi.fn(async () => ({ items: [] as unknown[] })),
}));
const { adminConnectionsMock } = vi.hoisted(() => ({
  adminConnectionsMock: vi.fn(async () => ({
    bridge: { connected: true, sync_healthy: true, status: "healthy" },
  })),
}));

vi.mock("../lib/api", () => ({
  api: {
    inbox: inboxMock,
    adminConnections: adminConnectionsMock,
  },
}));

describe("DashboardLayout", () => {
  beforeEach(() => {
    inboxMock.mockReset();
    inboxMock.mockResolvedValue({ items: [] });
    adminConnectionsMock.mockReset();
    adminConnectionsMock.mockResolvedValue({
      bridge: { connected: true, sync_healthy: true, status: "healthy" },
    });
  });

  it("shows Connections in navigation", async () => {
    render(
      <MemoryRouter initialEntries={["/connections"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "User menu" }));
    expect(await screen.findByRole("button", { name: "Connections" })).toBeInTheDocument();
  });

  it("renders three-zone shell layout landmarks", async () => {
    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    expect(await screen.findByTestId("shell-layout")).toBeInTheDocument();
    expect(screen.getByTestId("shell-sidebar")).toBeInTheDocument();
    expect(screen.getByTestId("shell-header")).toBeInTheDocument();
    expect(screen.getByTestId("shell-workspace")).toBeInTheDocument();
    expect(screen.getByTestId("shell-chat-slot")).toBeInTheDocument();
    expect(screen.getByText("child")).toBeInTheDocument();
  });

  it("renders inbox count as a separate badge even when count is zero", async () => {
    inboxMock.mockResolvedValue({ items: [] });

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    expect(await screen.findByText("Inbox")).toBeInTheDocument();
    const badges = await screen.findAllByText("0");
    expect(badges.length).toBeGreaterThanOrEqual(1);
    expect(badges[0]).toHaveClass("nav-badge");
  });

  it("renders non-zero inbox count in the badge", async () => {
    inboxMock.mockResolvedValue({ items: [{ id: "a" }, { id: "b" }, { id: "c" }] });

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    const badges = await screen.findAllByText("3");
    expect(badges.length).toBeGreaterThanOrEqual(1);
    expect(badges[0]).toHaveClass("nav-badge");
  });

  it("renders healthy bridge indicator without delay banner", async () => {
    adminConnectionsMock.mockResolvedValue({
      bridge: { connected: true, sync_healthy: true, status: "healthy" },
    });

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    expect(await screen.findByText("Bridge healthy")).toBeInTheDocument();
    expect(screen.queryByText("Messages may be delayed - bridge reconnecting")).not.toBeInTheDocument();
  });

  it("renders degraded bridge indicator and hides delay banner in local dev", async () => {
    adminConnectionsMock.mockResolvedValue({
      bridge: {
        connected: true,
        sync_healthy: false,
        status: "degraded",
        last_sync_age_seconds: 185,
      },
    });

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    expect(await screen.findByText("Bridge connected, OpenClaw unreachable")).toBeInTheDocument();
    expect(screen.queryByText("Bridge connected but OpenClaw unreachable")).not.toBeInTheDocument();
    expect(screen.queryByText("Last successful sync 3m ago")).not.toBeInTheDocument();
  });

  it("renders unhealthy bridge indicator and hides delay banner in local dev", async () => {
    adminConnectionsMock.mockResolvedValue({
      bridge: {
        connected: false,
        sync_healthy: false,
        status: "unhealthy",
        last_sync_age_seconds: 5400,
      },
    });

    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    expect(await screen.findByText("Bridge offline")).toBeInTheDocument();
    expect(screen.queryByText("Bridge offline - reconnecting")).not.toBeInTheDocument();
    expect(screen.queryByText("Last successful sync 1h ago")).not.toBeInTheDocument();
  });

  it("supports mobile sidebar and chat slot toggles while preserving shell stability", async () => {
    render(
      <MemoryRouter initialEntries={["/projects"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    const sidebar = await screen.findByTestId("shell-sidebar");
    const chatSlot = screen.getByTestId("shell-chat-slot");
    expect(chatSlot).not.toHaveClass("hidden");

    fireEvent.click(screen.getByRole("button", { name: "Toggle menu" }));
    expect(sidebar).toHaveClass("open");
    fireEvent.click(screen.getByRole("button", { name: "Close navigation" }));
    expect(sidebar).not.toHaveClass("open");

    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).toHaveClass("hidden");
    fireEvent.click(screen.getByRole("button", { name: "Toggle chat panel" }));
    expect(chatSlot).not.toHaveClass("hidden");

    fireEvent.click(screen.getByRole("link", { name: /Inbox/ }));
    expect(screen.getByTestId("shell-layout")).toBeInTheDocument();
  });
});
