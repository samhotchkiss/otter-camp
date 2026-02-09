import { useCallback, useEffect, useState } from "react";
import DataManagement from "../components/DataManagement";
import GitHubSettings from "./settings/GitHubSettings";

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type Profile = {
  name: string;
  email: string;
  avatarUrl: string | null;
};

type NotificationChannel = "email" | "push" | "inApp";

type NotificationEventType =
  | "taskAssigned"
  | "taskCompleted"
  | "mentions"
  | "comments"
  | "agentUpdates"
  | "weeklyDigest";

type NotificationPreferences = Record<
  NotificationEventType,
  Record<NotificationChannel, boolean>
>;

type WorkspaceMember = {
  id: string;
  name: string;
  email: string;
  role: "owner" | "admin" | "member";
  avatarUrl?: string;
};

type Workspace = {
  name: string;
  members: WorkspaceMember[];
};

type Integrations = {
  openclawWebhookUrl: string;
  apiKeys: Array<{
    id: string;
    name: string;
    prefix: string;
    createdAt: Date;
  }>;
};

type ManagedLabel = {
  id: string;
  name: string;
  color: string;
};

type ThemeMode = "light" | "dark" | "system";

const NOTIFICATION_EVENT_LABELS: Record<NotificationEventType, string> = {
  taskAssigned: "Task Assigned",
  taskCompleted: "Task Completed",
  mentions: "Mentions",
  comments: "Comments",
  agentUpdates: "Agent Updates",
  weeklyDigest: "Weekly Digest",
};

const NOTIFICATION_CHANNEL_LABELS: Record<NotificationChannel, string> = {
  email: "Email",
  push: "Push",
  inApp: "In-App",
};

const ROLE_LABELS: Record<WorkspaceMember["role"], string> = {
  owner: "Owner",
  admin: "Admin",
  member: "Member",
};

// ─────────────────────────────────────────────────────────────────────────────
// Form Components
// ─────────────────────────────────────────────────────────────────────────────

type InputProps = {
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: "text" | "email" | "url";
  placeholder?: string;
  disabled?: boolean;
  readOnly?: boolean;
};

function Input({
  label,
  value,
  onChange,
  type = "text",
  placeholder,
  disabled,
  readOnly,
}: InputProps) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-[var(--text)]">
        {label}
      </span>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        readOnly={readOnly}
        className="mt-1 block w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2.5 text-[var(--text)] shadow-sm transition placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:cursor-not-allowed disabled:bg-[var(--surface-alt)] disabled:text-[var(--text-muted)]"
      />
    </label>
  );
}

type ToggleProps = {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label?: string;
  disabled?: boolean;
};

function Toggle({ checked, onChange, label, disabled }: ToggleProps) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-3">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`relative h-6 w-11 rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--accent)] focus:ring-offset-2 focus:ring-offset-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-50 ${
          checked
            ? "bg-emerald-500"
            : "bg-[var(--border)]"
        }`}
      >
        <span
          className={`absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-[var(--surface)] shadow-sm transition-transform ${
            checked ? "translate-x-5" : "translate-x-0"
          }`}
        />
      </button>
      {label && (
        <span className="text-sm text-[var(--text)]">
          {label}
        </span>
      )}
    </label>
  );
}

type ButtonProps = {
  children: React.ReactNode;
  onClick?: () => void;
  variant?: "primary" | "secondary" | "danger";
  disabled?: boolean;
  loading?: boolean;
  type?: "button" | "submit";
};

function Button({
  children,
  onClick,
  variant = "primary",
  disabled,
  loading,
  type = "button",
}: ButtonProps) {
  const baseClasses =
    "inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2.5 text-sm font-medium shadow-sm transition focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-50";

  const variantClasses = {
    primary:
      "bg-emerald-500 text-white hover:bg-emerald-600 focus:ring-emerald-500",
    secondary:
      "border border-[var(--border)] bg-[var(--surface)] text-[var(--text)] hover:bg-[var(--surface-alt)] focus:ring-[var(--accent)]",
    danger:
      "bg-red-500 text-white hover:bg-red-600 focus:ring-red-500",
  };

  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled || loading}
      className={`${baseClasses} ${variantClasses[variant]}`}
    >
      {loading && (
        <svg
          className="h-4 w-4 animate-spin"
          fill="none"
          viewBox="0 0 24 24"
        >
          <circle
            className="opacity-25"
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            strokeWidth="4"
          />
          <path
            className="opacity-75"
            fill="currentColor"
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
          />
        </svg>
      )}
      {children}
    </button>
  );
}

type SectionCardProps = {
  title: string;
  description?: string;
  icon?: string;
  children: React.ReactNode;
};

function SectionCard({ title, description, icon, children }: SectionCardProps) {
  return (
    <section className="overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-sm backdrop-blur">
      <div className="border-b border-[var(--border)] px-6 py-4">
        <div className="flex items-center gap-3">
          {icon && (
            <span className="text-2xl" aria-hidden="true">
              {icon}
            </span>
          )}
          <div>
            <h2 className="text-lg font-semibold text-[var(--text)]">
              {title}
            </h2>
            {description && (
              <p className="mt-0.5 text-sm text-[var(--text-muted)]">
                {description}
              </p>
            )}
          </div>
        </div>
      </div>
      <div className="p-6">{children}</div>
    </section>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Section Components
// ─────────────────────────────────────────────────────────────────────────────

type ProfileSectionProps = {
  profile: Profile;
  onUpdate: (profile: Profile) => void;
  onSave: () => Promise<void>;
  saving: boolean;
};

function ProfileSection({
  profile,
  onUpdate,
  onSave,
  saving,
}: ProfileSectionProps) {
  const handleAvatarUpload = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0];
      if (file) {
        const reader = new FileReader();
        reader.onloadend = () => {
          onUpdate({ ...profile, avatarUrl: reader.result as string });
        };
        reader.readAsDataURL(file);
      }
    },
    [profile, onUpdate]
  );

  const getInitials = (name: string): string => {
    return name
      .split(" ")
      .map((part) => part[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  };

  return (
    <SectionCard
      title="Profile"
      description="Manage your personal information"
      icon="👤"
    >
      <div className="space-y-6">
        {/* Avatar Upload */}
        <div className="flex items-center gap-6">
          <div className="relative">
            {profile.avatarUrl ? (
              <img
                src={profile.avatarUrl}
                alt="Avatar"
                loading="lazy"
                decoding="async"
                className="h-20 w-20 rounded-full object-cover"
              />
            ) : (
              <div className="flex h-20 w-20 items-center justify-center rounded-full bg-emerald-100 text-2xl font-semibold text-emerald-700 dark:bg-emerald-900/50 dark:text-emerald-200">
                {getInitials(profile.name || "OC")}
              </div>
            )}
            <label className="absolute -bottom-1 -right-1 cursor-pointer rounded-full bg-[var(--surface)] p-2 shadow-md transition hover:bg-[var(--surface-alt)]">
              <svg
                className="h-4 w-4 text-[var(--text-muted)]"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M3 9a2 2 0 012-2h.93a2 2 0 001.664-.89l.812-1.22A2 2 0 0110.07 4h3.86a2 2 0 011.664.89l.812 1.22A2 2 0 0018.07 7H19a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2V9z"
                />
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M15 13a3 3 0 11-6 0 3 3 0 016 0z"
                />
              </svg>
              <input
                type="file"
                accept="image/*"
                onChange={handleAvatarUpload}
                className="hidden"
              />
            </label>
          </div>
          <div className="text-sm text-[var(--text-muted)]">
            <p>Upload a new avatar</p>
            <p className="mt-1 text-xs">JPG, PNG, GIF. Max 2MB.</p>
          </div>
        </div>

        {/* Name & Email */}
        <div className="grid gap-4 sm:grid-cols-2">
          <Input
            label="Display Name"
            value={profile.name}
            onChange={(name) => onUpdate({ ...profile, name })}
            placeholder="Your name"
          />
          <Input
            label="Email Address"
            type="email"
            value={profile.email}
            onChange={(email) => onUpdate({ ...profile, email })}
            placeholder="you@example.com"
          />
        </div>

        <div className="flex justify-end">
          <Button onClick={onSave} loading={saving}>
            Save Profile
          </Button>
        </div>
      </div>
    </SectionCard>
  );
}

type NotificationsSectionProps = {
  preferences: NotificationPreferences;
  onUpdate: (preferences: NotificationPreferences) => void;
  onSave: () => Promise<void>;
  saving: boolean;
};

function NotificationsSection({
  preferences,
  onUpdate,
  onSave,
  saving,
}: NotificationsSectionProps) {
  const handleToggle = useCallback(
    (event: NotificationEventType, channel: NotificationChannel) => {
      onUpdate({
        ...preferences,
        [event]: {
          ...preferences[event],
          [channel]: !preferences[event][channel],
        },
      });
    },
    [preferences, onUpdate]
  );

  const events = Object.keys(NOTIFICATION_EVENT_LABELS) as NotificationEventType[];
  const channels = Object.keys(NOTIFICATION_CHANNEL_LABELS) as NotificationChannel[];

  return (
    <SectionCard
      title="Notifications"
      description="Choose how you want to be notified"
      icon="🔔"
    >
      <div className="space-y-6">
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-[var(--border)]">
                <th className="pb-3 text-left text-sm font-medium text-[var(--text-muted)]">
                  Event Type
                </th>
                {channels.map((channel) => (
                  <th
                    key={channel}
                    className="pb-3 text-center text-sm font-medium text-[var(--text-muted)]"
                  >
                    {NOTIFICATION_CHANNEL_LABELS[channel]}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border)]/60">
              {events.map((event) => (
                <tr key={event}>
                  <td className="py-4 text-sm font-medium text-[var(--text)]">
                    {NOTIFICATION_EVENT_LABELS[event]}
                  </td>
                  {channels.map((channel) => (
                    <td key={channel} className="py-4 text-center">
                      <Toggle
                        checked={preferences[event][channel]}
                        onChange={() => handleToggle(event, channel)}
                      />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="flex justify-end">
          <Button onClick={onSave} loading={saving}>
            Save Preferences
          </Button>
        </div>
      </div>
    </SectionCard>
  );
}

type WorkspaceSectionProps = {
  workspace: Workspace;
  onUpdate: (workspace: Workspace) => void;
  onSave: () => Promise<void>;
  saving: boolean;
};

function WorkspaceSection({
  workspace,
  onUpdate,
  onSave,
  saving,
}: WorkspaceSectionProps) {
  const getInitials = (name: string): string => {
    return name
      .split(" ")
      .map((part) => part[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  };

  return (
    <SectionCard
      title="Workspace"
      description="Manage your workspace settings and members"
      icon="🏕️"
    >
      <div className="space-y-6">
        {/* Workspace Name */}
        <Input
          label="Workspace Name"
          value={workspace.name}
          onChange={(name) => onUpdate({ ...workspace, name })}
          placeholder="My Workspace"
        />

        {/* Members List */}
        <div>
          <h3 className="text-sm font-medium text-[var(--text)]">
            Members ({workspace.members.length})
          </h3>
          <div className="mt-3 divide-y divide-[var(--border)]/60 rounded-lg border border-[var(--border)]">
            {workspace.members.map((member) => (
              <div
                key={member.id}
                className="flex items-center justify-between px-4 py-3"
              >
                <div className="flex items-center gap-3">
                  {member.avatarUrl ? (
                    <img
                      src={member.avatarUrl}
                      alt={member.name}
                      loading="lazy"
                      decoding="async"
                      className="h-10 w-10 rounded-full object-cover"
                    />
                  ) : (
                    <div className="flex h-10 w-10 items-center justify-center rounded-full bg-[var(--surface-alt)] text-sm font-semibold text-[var(--text-muted)]">
                      {getInitials(member.name)}
                    </div>
                  )}
                  <div>
                    <p className="font-medium text-[var(--text)]">
                      {member.name}
                    </p>
                    <p className="text-sm text-[var(--text-muted)]">
                      {member.email}
                    </p>
                  </div>
                </div>
                <span
                  className={`rounded-full px-3 py-1 text-xs font-medium ${
                    member.role === "owner"
                      ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/50 dark:text-emerald-200"
                      : member.role === "admin"
                        ? "bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-200"
                        : "bg-[var(--surface-alt)] text-[var(--text-muted)]"
                  }`}
                >
                  {ROLE_LABELS[member.role]}
                </span>
              </div>
            ))}
          </div>
        </div>

        <div className="flex justify-end gap-3">
          <Button variant="secondary">Invite Member</Button>
          <Button onClick={onSave} loading={saving}>
            Save Workspace
          </Button>
        </div>
      </div>
    </SectionCard>
  );
}

type IntegrationsSectionProps = {
  integrations: Integrations;
  onUpdate: (integrations: Integrations) => void;
  onSave: () => Promise<void>;
  onGenerateApiKey: () => Promise<void>;
  onRevokeApiKey: (keyId: string) => Promise<void>;
  saving: boolean;
};

function IntegrationsSection({
  integrations,
  onUpdate,
  onSave,
  onGenerateApiKey,
  onRevokeApiKey,
  saving,
}: IntegrationsSectionProps) {
  const [generatingKey, setGeneratingKey] = useState(false);
  const [revokingKey, setRevokingKey] = useState<string | null>(null);

  const handleGenerateKey = async () => {
    setGeneratingKey(true);
    try {
      await onGenerateApiKey();
    } finally {
      setGeneratingKey(false);
    }
  };

  const handleRevokeKey = async (keyId: string) => {
    setRevokingKey(keyId);
    try {
      await onRevokeApiKey(keyId);
    } finally {
      setRevokingKey(null);
    }
  };

  const formatDate = (date: Date): string => {
    return new Intl.DateTimeFormat("en-US", {
      dateStyle: "medium",
    }).format(date);
  };

  return (
    <SectionCard
      title="Integrations"
      description="Connect external services and manage API access"
      icon="🔗"
    >
      <div className="space-y-6">
        {/* OpenClaw Webhook URL */}
        <div>
          <Input
            label="OpenClaw Webhook URL"
            type="url"
            value={integrations.openclawWebhookUrl}
            onChange={(openclawWebhookUrl) =>
              onUpdate({ ...integrations, openclawWebhookUrl })
            }
            placeholder="https://your-openclaw-instance.com/webhook"
          />
          <p className="mt-1.5 text-xs text-[var(--text-muted)]">
            Events will be sent to this URL when triggered
          </p>
        </div>

        {/* API Keys */}
        <div>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-[var(--text)]">
              API Keys
            </h3>
            <Button
              variant="secondary"
              onClick={handleGenerateKey}
              loading={generatingKey}
            >
              Generate New Key
            </Button>
          </div>

          {integrations.apiKeys.length > 0 ? (
            <div className="mt-3 divide-y divide-[var(--border)]/60 rounded-lg border border-[var(--border)]">
              {integrations.apiKeys.map((key) => (
                <div
                  key={key.id}
                  className="flex items-center justify-between px-4 py-3"
                >
                  <div>
                    <p className="font-medium text-[var(--text)]">
                      {key.name}
                    </p>
                    <p className="mt-0.5 font-mono text-sm text-[var(--text-muted)]">
                      {key.prefix}••••••••
                    </p>
                    <p className="mt-1 text-xs text-[var(--text-muted)]">
                      Created {formatDate(key.createdAt)}
                    </p>
                  </div>
                  <Button
                    variant="danger"
                    onClick={() => handleRevokeKey(key.id)}
                    loading={revokingKey === key.id}
                  >
                    Revoke
                  </Button>
                </div>
              ))}
            </div>
          ) : (
            <div className="mt-3 rounded-lg border border-dashed border-[var(--border)] bg-[var(--surface-alt)] px-6 py-8 text-center">
              <p className="text-sm text-[var(--text-muted)]">
                No API keys yet
              </p>
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                Generate a key to access the API programmatically
              </p>
            </div>
          )}
        </div>

        <div className="flex justify-end">
          <Button onClick={onSave} loading={saving}>
            Save Integrations
          </Button>
        </div>
      </div>
    </SectionCard>
  );
}

type LabelManagementSectionProps = {
  labels: ManagedLabel[];
  drafts: Record<string, { name: string; color: string }>;
  loading: boolean;
  error: string | null;
  newLabelName: string;
  newLabelColor: string;
  creating: boolean;
  savingLabelID: string | null;
  deletingLabelID: string | null;
  onNewLabelNameChange: (value: string) => void;
  onNewLabelColorChange: (value: string) => void;
  onDraftChange: (labelID: string, field: "name" | "color", value: string) => void;
  onCreate: () => Promise<void>;
  onSave: (label: ManagedLabel) => Promise<void>;
  onDelete: (label: ManagedLabel) => Promise<void>;
};

function LabelManagementSection({
  labels,
  drafts,
  loading,
  error,
  newLabelName,
  newLabelColor,
  creating,
  savingLabelID,
  deletingLabelID,
  onNewLabelNameChange,
  onNewLabelColorChange,
  onDraftChange,
  onCreate,
  onSave,
  onDelete,
}: LabelManagementSectionProps) {
  return (
    <SectionCard
      title="Label Management"
      description="Manage reusable project and issue labels"
      icon="🏷️"
    >
      <div className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-end">
          <Input
            label="New label name"
            value={newLabelName}
            onChange={onNewLabelNameChange}
            placeholder="e.g. blocked"
          />
          <label className="block">
            <span className="text-sm font-medium text-[var(--text)]">
              New label color
            </span>
            <input
              aria-label="New label color"
              type="color"
              value={newLabelColor}
              onChange={(event) => onNewLabelColorChange(event.target.value)}
              className="mt-1 h-11 w-16 cursor-pointer rounded-lg border border-[var(--border)] bg-[var(--surface)] px-1 py-1"
            />
          </label>
          <Button onClick={() => void onCreate()} loading={creating}>
            Create Label
          </Button>
        </div>

        {loading && (
          <p className="text-sm text-[var(--text-muted)]">Loading labels…</p>
        )}
        {error && (
          <p className="text-sm text-red-700">{error}</p>
        )}

        {!loading && labels.length === 0 && (
          <p className="text-sm text-[var(--text-muted)]">
            No labels yet. Create one above to get started.
          </p>
        )}

        {!loading && labels.length > 0 && (
          <div className="space-y-2">
            {labels.map((label) => {
              const draft = drafts[label.id] ?? { name: label.name, color: label.color };
              const dirty =
                draft.name.trim() !== label.name || draft.color.toLowerCase() !== label.color.toLowerCase();
              return (
                <div
                  key={label.id}
                  data-testid={`label-row-${label.id}`}
                  className="grid gap-2 rounded-lg border border-[var(--border)] p-3 sm:grid-cols-[1fr_auto_auto_auto]"
                >
                  <input
                    aria-label={`Label name ${label.name}`}
                    data-testid={`label-name-${label.id}`}
                    value={draft.name}
                    onChange={(event) => onDraftChange(label.id, "name", event.target.value)}
                    className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)]"
                  />
                  <input
                    aria-label={`Label color ${label.name}`}
                    data-testid={`label-color-${label.id}`}
                    type="color"
                    value={draft.color}
                    onChange={(event) => onDraftChange(label.id, "color", event.target.value)}
                    className="h-10 w-16 cursor-pointer rounded-lg border border-[var(--border)] bg-[var(--surface)] px-1 py-1"
                  />
                  <Button
                    variant="secondary"
                    disabled={!dirty}
                    loading={savingLabelID === label.id}
                    onClick={() => void onSave(label)}
                  >
                    Save
                  </Button>
                  <Button
                    variant="danger"
                    loading={deletingLabelID === label.id}
                    onClick={() => void onDelete(label)}
                  >
                    Delete
                  </Button>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </SectionCard>
  );
}

type AppearanceSectionProps = {
  theme: ThemeMode;
  onUpdate: (theme: ThemeMode) => void;
};

function AppearanceSection({ theme, onUpdate }: AppearanceSectionProps) {
  const themes: Array<{ value: ThemeMode; label: string; icon: string }> = [
    { value: "light", label: "Light", icon: "☀️" },
    { value: "dark", label: "Dark", icon: "🌙" },
    { value: "system", label: "System", icon: "💻" },
  ];

  return (
    <SectionCard
      title="Appearance"
      description="Customize how Otter Camp looks"
      icon="🎨"
    >
      <div className="space-y-4">
        <p className="text-sm text-[var(--text-muted)]">
          Choose your preferred theme
        </p>

        <div className="grid gap-3 sm:grid-cols-3">
          {themes.map(({ value, label, icon }) => (
            <button
              key={value}
              type="button"
              onClick={() => onUpdate(value)}
              className={`flex flex-col items-center gap-2 rounded-xl border-2 px-6 py-4 transition ${
                theme === value
                  ? "border-emerald-500 bg-emerald-50 dark:border-emerald-500 dark:bg-emerald-900/20"
                  : "border-[var(--border)] bg-[var(--surface)] hover:border-[var(--accent)]/50 hover:bg-[var(--surface-alt)]"
              }`}
            >
              <span className="text-2xl">{icon}</span>
              <span
                className={`text-sm font-medium ${
                  theme === value
                    ? "text-emerald-700 dark:text-emerald-200"
                    : "text-[var(--text)]"
                }`}
              >
                {label}
              </span>
            </button>
          ))}
        </div>
      </div>
    </SectionCard>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Main Settings Page
// ─────────────────────────────────────────────────────────────────────────────

const DEFAULT_NOTIFICATION_PREFS: NotificationPreferences = {
  taskAssigned: { email: true, push: true, inApp: true },
  taskCompleted: { email: false, push: true, inApp: true },
  mentions: { email: true, push: true, inApp: true },
  comments: { email: false, push: false, inApp: true },
  agentUpdates: { email: false, push: true, inApp: true },
  weeklyDigest: { email: true, push: false, inApp: false },
};

// Removed outer layout wrapper - now uses DashboardLayout from router
export default function SettingsPage() {
  // Profile state
  const [profile, setProfile] = useState<Profile>({
    name: "",
    email: "",
    avatarUrl: null,
  });
  const [savingProfile, setSavingProfile] = useState(false);

  // Notifications state
  const [notifications, setNotifications] = useState<NotificationPreferences>(
    DEFAULT_NOTIFICATION_PREFS
  );
  const [savingNotifications, setSavingNotifications] = useState(false);

  // Workspace state
  const [workspace, setWorkspace] = useState<Workspace>({
    name: "",
    members: [],
  });
  const [savingWorkspace, setSavingWorkspace] = useState(false);

  // Integrations state
  const [integrations, setIntegrations] = useState<Integrations>({
    openclawWebhookUrl: "",
    apiKeys: [],
  });
  const [savingIntegrations, setSavingIntegrations] = useState(false);

  // Labels state
  const [labels, setLabels] = useState<ManagedLabel[]>([]);
  const [labelDrafts, setLabelDrafts] = useState<Record<string, { name: string; color: string }>>({});
  const [loadingLabels, setLoadingLabels] = useState(false);
  const [labelError, setLabelError] = useState<string | null>(null);
  const [newLabelName, setNewLabelName] = useState("");
  const [newLabelColor, setNewLabelColor] = useState("#6b7280");
  const [creatingLabel, setCreatingLabel] = useState(false);
  const [savingLabelID, setSavingLabelID] = useState<string | null>(null);
  const [deletingLabelID, setDeletingLabelID] = useState<string | null>(null);

  // Theme state
  const [theme, setTheme] = useState<ThemeMode>("system");

  const upsertLabelDrafts = useCallback((items: ManagedLabel[]) => {
    setLabelDrafts((existing) => {
      const next: Record<string, { name: string; color: string }> = {};
      for (const label of items) {
        const prior = existing[label.id];
        next[label.id] = prior ?? { name: label.name, color: label.color };
      }
      return next;
    });
  }, []);

  // Load settings on mount
  useEffect(() => {
    const loadSettings = async () => {
      try {
        const [profileRes, notificationsRes, workspaceRes, integrationsRes] =
          await Promise.all([
            fetch("/api/settings/profile"),
            fetch("/api/settings/notifications"),
            fetch("/api/settings/workspace"),
            fetch("/api/settings/integrations"),
          ]);

        if (profileRes.ok) {
          const data = await profileRes.json();
          setProfile(data);
        }

        if (notificationsRes.ok) {
          const data = await notificationsRes.json();
          setNotifications(data);
        }

        if (workspaceRes.ok) {
          const data = await workspaceRes.json();
          setWorkspace(data);
        }

        if (integrationsRes.ok) {
          const data = await integrationsRes.json();
          setIntegrations({
            ...data,
            apiKeys: data.apiKeys.map((key: { createdAt: string }) => ({
              ...key,
              createdAt: new Date(key.createdAt),
            })),
          });
        }

        // Load theme from localStorage
        const savedTheme = localStorage.getItem("otter-camp-theme") as ThemeMode;
        if (savedTheme) {
          setTheme(savedTheme);
        }
      } catch (error) {
        console.error("Failed to load settings:", error);
      }
    };

    loadSettings();
  }, []);

  useEffect(() => {
    const orgID = localStorage.getItem("otter-camp-org-id") || "";
    if (!orgID) {
      setLabelError("Missing organization context");
      setLoadingLabels(false);
      return;
    }

    let cancelled = false;
    setLoadingLabels(true);
    setLabelError(null);

    void fetch(`/api/labels?org_id=${encodeURIComponent(orgID)}&seed=true`)
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load labels");
        }
        return response.json() as Promise<{ labels: ManagedLabel[] }>;
      })
      .then((payload) => {
        if (cancelled) {
          return;
        }
        const list = Array.isArray(payload.labels) ? payload.labels : [];
        const sorted = [...list].sort((a, b) => a.name.localeCompare(b.name));
        setLabels(sorted);
        upsertLabelDrafts(sorted);
      })
      .catch((err: unknown) => {
        if (cancelled) {
          return;
        }
        setLabelError(err instanceof Error ? err.message : "Failed to load labels");
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingLabels(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [upsertLabelDrafts]);

  // Apply theme changes
  useEffect(() => {
    localStorage.setItem("otter-camp-theme", theme);

    const root = document.documentElement;
    root.classList.remove("dark", "light");

    if (theme === "system") {
      const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
      root.classList.toggle("dark", prefersDark);
    } else {
      root.classList.toggle("dark", theme === "dark");
    }
  }, [theme]);

  // Save handlers
  const handleSaveProfile = useCallback(async () => {
    setSavingProfile(true);
    try {
      await fetch("/api/settings/profile", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(profile),
      });
    } finally {
      setSavingProfile(false);
    }
  }, [profile]);

  const handleSaveNotifications = useCallback(async () => {
    setSavingNotifications(true);
    try {
      await fetch("/api/settings/notifications", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(notifications),
      });
    } finally {
      setSavingNotifications(false);
    }
  }, [notifications]);

  const handleSaveWorkspace = useCallback(async () => {
    setSavingWorkspace(true);
    try {
      await fetch("/api/settings/workspace", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(workspace),
      });
    } finally {
      setSavingWorkspace(false);
    }
  }, [workspace]);

  const handleSaveIntegrations = useCallback(async () => {
    setSavingIntegrations(true);
    try {
      await fetch("/api/settings/integrations", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(integrations),
      });
    } finally {
      setSavingIntegrations(false);
    }
  }, [integrations]);

  const handleGenerateApiKey = useCallback(async () => {
    const response = await fetch("/api/settings/integrations/api-keys", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: `API Key ${integrations.apiKeys.length + 1}` }),
    });

    if (response.ok) {
      const newKey = await response.json();
      setIntegrations((prev) => ({
        ...prev,
        apiKeys: [
          ...prev.apiKeys,
          { ...newKey, createdAt: new Date(newKey.createdAt) },
        ],
      }));
    }
  }, [integrations.apiKeys.length]);

  const handleRevokeApiKey = useCallback(async (keyId: string) => {
    await fetch(`/api/settings/integrations/api-keys/${keyId}`, {
      method: "DELETE",
    });

    setIntegrations((prev) => ({
      ...prev,
      apiKeys: prev.apiKeys.filter((key) => key.id !== keyId),
    }));
  }, []);

  const handleLabelDraftChange = useCallback(
    (labelID: string, field: "name" | "color", value: string) => {
      setLabelDrafts((prev) => ({
        ...prev,
        [labelID]: {
          ...(prev[labelID] ?? { name: "", color: "#6b7280" }),
          [field]: value,
        },
      }));
    },
    [],
  );

  const handleCreateLabel = useCallback(async () => {
    const orgID = localStorage.getItem("otter-camp-org-id") || "";
    const name = newLabelName.trim();
    if (!orgID || !name) {
      return;
    }
    setCreatingLabel(true);
    setLabelError(null);
    try {
      const response = await fetch(`/api/labels?org_id=${encodeURIComponent(orgID)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, color: newLabelColor }),
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to create label");
      }
      const created = await response.json() as ManagedLabel;
      setLabels((prev) => {
        const next = [...prev, created].sort((a, b) => a.name.localeCompare(b.name));
        return next;
      });
      upsertLabelDrafts([created]);
      setNewLabelName("");
      setNewLabelColor("#6b7280");
    } catch (err) {
      setLabelError(err instanceof Error ? err.message : "Failed to create label");
    } finally {
      setCreatingLabel(false);
    }
  }, [newLabelColor, newLabelName, upsertLabelDrafts]);

  const handleSaveLabel = useCallback(async (label: ManagedLabel) => {
    const orgID = localStorage.getItem("otter-camp-org-id") || "";
    if (!orgID) {
      return;
    }
    const draft = labelDrafts[label.id] ?? { name: label.name, color: label.color };
    const nextName = draft.name.trim();
    if (!nextName) {
      return;
    }
    setSavingLabelID(label.id);
    setLabelError(null);
    try {
      const response = await fetch(
        `/api/labels/${encodeURIComponent(label.id)}?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: nextName, color: draft.color }),
        },
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to save label");
      }
      const updated = await response.json() as ManagedLabel;
      setLabels((prev) =>
        prev
          .map((existing) => (existing.id === updated.id ? updated : existing))
          .sort((a, b) => a.name.localeCompare(b.name)),
      );
      setLabelDrafts((prev) => ({
        ...prev,
        [updated.id]: { name: updated.name, color: updated.color },
      }));
    } catch (err) {
      setLabelError(err instanceof Error ? err.message : "Failed to save label");
    } finally {
      setSavingLabelID(null);
    }
  }, [labelDrafts]);

  const handleDeleteLabel = useCallback(async (label: ManagedLabel) => {
    const orgID = localStorage.getItem("otter-camp-org-id") || "";
    if (!orgID) {
      return;
    }
    const confirmed = window.confirm(
      `Delete label "${label.name}"? This will remove it from linked projects and issues.`,
    );
    if (!confirmed) {
      return;
    }
    setDeletingLabelID(label.id);
    setLabelError(null);
    try {
      const response = await fetch(
        `/api/labels/${encodeURIComponent(label.id)}?org_id=${encodeURIComponent(orgID)}`,
        { method: "DELETE" },
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to delete label");
      }
      setLabels((prev) => prev.filter((existing) => existing.id !== label.id));
      setLabelDrafts((prev) => {
        const next = { ...prev };
        delete next[label.id];
        return next;
      });
    } catch (err) {
      setLabelError(err instanceof Error ? err.message : "Failed to delete label");
    } finally {
      setDeletingLabelID(null);
    }
  }, []);

  return (
    <div className="w-full">
      {/* Header */}
      <div className="mb-8">
        <h1 className="text-3xl font-semibold tracking-tight text-[var(--text)]">
          ⚙️ Settings
        </h1>
        <p className="mt-2 text-[var(--text-muted)]">
          Manage your account, preferences, and integrations
        </p>
      </div>

      {/* Settings Sections */}
      <div className="space-y-8">
        <ProfileSection
          profile={profile}
          onUpdate={setProfile}
          onSave={handleSaveProfile}
          saving={savingProfile}
        />

        <NotificationsSection
          preferences={notifications}
          onUpdate={setNotifications}
          onSave={handleSaveNotifications}
          saving={savingNotifications}
        />

        <WorkspaceSection
          workspace={workspace}
          onUpdate={setWorkspace}
          onSave={handleSaveWorkspace}
          saving={savingWorkspace}
        />

        <IntegrationsSection
          integrations={integrations}
          onUpdate={setIntegrations}
          onSave={handleSaveIntegrations}
          onGenerateApiKey={handleGenerateApiKey}
          onRevokeApiKey={handleRevokeApiKey}
          saving={savingIntegrations}
        />

        <LabelManagementSection
          labels={labels}
          drafts={labelDrafts}
          loading={loadingLabels}
          error={labelError}
          newLabelName={newLabelName}
          newLabelColor={newLabelColor}
          creating={creatingLabel}
          savingLabelID={savingLabelID}
          deletingLabelID={deletingLabelID}
          onNewLabelNameChange={setNewLabelName}
          onNewLabelColorChange={setNewLabelColor}
          onDraftChange={handleLabelDraftChange}
          onCreate={handleCreateLabel}
          onSave={handleSaveLabel}
          onDelete={handleDeleteLabel}
        />

        <GitHubSettings />

        <AppearanceSection theme={theme} onUpdate={setTheme} />

        {/* Data Management - Export/Import */}
        <DataManagement orgId={localStorage.getItem("otter-camp-org-id") || ""} />
      </div>
    </div>
  );
}
