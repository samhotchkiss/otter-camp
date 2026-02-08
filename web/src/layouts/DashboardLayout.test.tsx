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

vi.mock("../lib/api", () => ({
  api: {
    inbox: inboxMock,
  },
}));

describe("DashboardLayout", () => {
  beforeEach(() => {
    inboxMock.mockReset();
    inboxMock.mockResolvedValue({ items: [] });
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
});
