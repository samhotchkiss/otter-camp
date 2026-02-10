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

  it("renders degraded bridge indicator and delayed-message banner", async () => {
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
    expect(await screen.findByText("Bridge connected but OpenClaw unreachable")).toBeInTheDocument();
    expect(await screen.findByText("Last successful sync 3m ago")).toBeInTheDocument();
  });

  it("renders unhealthy bridge indicator and delayed-message banner", async () => {
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
    expect(await screen.findByText("Bridge offline - reconnecting")).toBeInTheDocument();
    expect(await screen.findByText("Last successful sync 1h ago")).toBeInTheDocument();
  });
});
