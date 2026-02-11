import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import LoginPage from "./LoginPage";

const { mockLogin } = vi.hoisted(() => ({
  mockLogin: vi.fn(),
}));

vi.mock("../contexts/AuthContext", () => ({
  useAuth: () => ({
    login: mockLogin,
  }),
}));

describe("LoginPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("defaults to local onboarding mode", () => {
    render(<LoginPage />);

    const localButton = screen.getByRole("button", { name: /local setup/i });
    const hostedButton = screen.getByRole("button", { name: /hosted setup/i });

    expect(localButton).toHaveAttribute("aria-pressed", "true");
    expect(hostedButton).toHaveAttribute("aria-pressed", "false");

    expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/organization/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/email address/i)).toBeInTheDocument();
  });

  it("shows hosted setup guidance when hosted mode is selected", async () => {
    const user = userEvent.setup();
    render(<LoginPage />);

    await user.click(screen.getByRole("button", { name: /hosted setup/i }));

    expect(screen.getByText(/hosted onboarding is moving to otter\.camp\/setup/i)).toBeInTheDocument();

    const hostedLink = screen.getByRole("link", { name: /go to hosted setup/i });
    expect(hostedLink).toHaveAttribute("href", "https://otter.camp/setup");

    expect(screen.queryByLabelText(/name/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/email address/i)).not.toBeInTheDocument();
  });

  it("shows decision-aligned copy for local and hosted paths", async () => {
    const user = userEvent.setup();
    render(<LoginPage />);

    expect(screen.getByText(/local setup is available now via magic link/i)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /hosted setup/i }));

    expect(screen.getByText(/hosted setup is deferred for now/i)).toBeInTheDocument();
  });

  it("submits local mode via login", async () => {
    const user = userEvent.setup();
    let resolveLogin: (() => void) | null = null;
    mockLogin.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveLogin = resolve;
        }),
    );

    render(<LoginPage />);

    await user.click(screen.getByRole("button", { name: /hosted setup/i }));
    await user.click(screen.getByRole("button", { name: /local setup/i }));

    await user.type(screen.getByLabelText(/name/i), "Sam");
    await user.type(screen.getByLabelText(/organization/i), "Otter Camp");
    await user.type(screen.getByLabelText(/email address/i), "sam@example.com");
    await user.click(screen.getByRole("button", { name: /generate magic link/i }));

    expect(mockLogin).toHaveBeenCalledWith("sam@example.com", "Sam", "Otter Camp");
    expect(screen.getByRole("button", { name: /sending/i })).toBeDisabled();

    resolveLogin?.();
    expect(await screen.findByText(/check your email/i)).toBeInTheDocument();
  });

  it("shows magic-link success state after local submit", async () => {
    const user = userEvent.setup();
    mockLogin.mockResolvedValueOnce(undefined);

    render(<LoginPage />);

    await user.type(screen.getByLabelText(/email address/i), "sam@example.com");
    await user.click(screen.getByRole("button", { name: /generate magic link/i }));

    expect(await screen.findByText(/check your email/i)).toBeInTheDocument();
    expect(screen.getByText(/we generated a magic link for/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /use a different email/i })).toBeInTheDocument();
  });

  it("shows API error when local submit fails", async () => {
    const user = userEvent.setup();
    mockLogin.mockRejectedValueOnce(new Error("Invalid login request"));

    render(<LoginPage />);

    await user.type(screen.getByLabelText(/email address/i), "sam@example.com");
    await user.click(screen.getByRole("button", { name: /generate magic link/i }));

    expect(await screen.findByText("Invalid login request")).toBeInTheDocument();
  });
});
