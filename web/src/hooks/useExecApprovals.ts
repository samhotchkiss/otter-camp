import { useCallback, useEffect, useMemo, useState } from "react";
import { useWS } from "../contexts/WebSocketContext";

const API_BASE = import.meta.env.VITE_API_URL || "";

export type ExecApprovalDecision = "approve" | "deny";

export type ExecApprovalRequest = {
  id: string;
  org_id: string;
  status: string;
  command: string;
  created_at: string;
  external_id?: string;
  agent_id?: string;
  task_id?: string;
  cwd?: string;
  shell?: string;
  args?: unknown;
  env?: unknown;
  message?: string;
  callback_url?: string;
  request?: unknown;
  response?: unknown;
  resolved_at?: string;
};

type ExecApprovalListResponse = {
  org_id?: string;
  status?: string;
  limit?: number;
  requests?: unknown;
};

const parseExecApproval = (raw: unknown): ExecApprovalRequest | null => {
  if (!raw || typeof raw !== "object") return null;
  const record = raw as Record<string, unknown>;

  const id = typeof record.id === "string" ? record.id : "";
  const org_id = typeof record.org_id === "string" ? record.org_id : "";
  const status = typeof record.status === "string" ? record.status : "pending";
  const command = typeof record.command === "string" ? record.command : "";
  const created_at =
    typeof record.created_at === "string"
      ? record.created_at
      : new Date().toISOString();

  if (!id || !org_id || !command) return null;

  return {
    id,
    org_id,
    status,
    command,
    created_at,
    external_id:
      typeof record.external_id === "string" ? record.external_id : undefined,
    agent_id: typeof record.agent_id === "string" ? record.agent_id : undefined,
    task_id: typeof record.task_id === "string" ? record.task_id : undefined,
    cwd: typeof record.cwd === "string" ? record.cwd : undefined,
    shell: typeof record.shell === "string" ? record.shell : undefined,
    args: record.args,
    env: record.env,
    message: typeof record.message === "string" ? record.message : undefined,
    callback_url:
      typeof record.callback_url === "string" ? record.callback_url : undefined,
    request: record.request,
    response: record.response,
    resolved_at:
      typeof record.resolved_at === "string" ? record.resolved_at : undefined,
  };
};

type UseExecApprovalsResult = {
  orgId: string | null;
  requests: ExecApprovalRequest[];
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  respond: (
    id: string,
    decision: ExecApprovalDecision,
    comment?: string,
  ) => Promise<ExecApprovalRequest>;
};

export default function useExecApprovals(orgId?: string): UseExecApprovalsResult {
  const { lastMessage } = useWS();
  const resolvedOrgId = useMemo(() => {
    const trimmed = (orgId ?? "").trim();
    if (trimmed) return trimmed;
    try {
      return (localStorage.getItem("otter-camp-org-id") ?? "").trim();
    } catch {
      return "";
    }
  }, [orgId]);

  const [requests, setRequests] = useState<ExecApprovalRequest[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    const org = resolvedOrgId.trim();
    if (!org) {
      setRequests([]);
      setError(null);
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const url = new URL(`${API_BASE}/api/approvals/exec`, window.location.origin);
      url.searchParams.set("org_id", org);
      url.searchParams.set("status", "pending");
      url.searchParams.set("limit", "200");

      const response = await fetch(url.toString(), {
        headers: { "Content-Type": "application/json" },
      });
      if (!response.ok) {
        const payload = (await response.json().catch(() => null)) as
          | { error?: string }
          | null;
        throw new Error(payload?.error || "Failed to load approvals");
      }

      const data = (await response.json()) as ExecApprovalListResponse;
      const parsed = Array.isArray(data.requests)
        ? (data.requests as unknown[])
            .map(parseExecApproval)
            .filter((r): r is ExecApprovalRequest => r !== null)
        : [];

      setRequests(parsed);
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to load approvals";
      setError(message);
    } finally {
      setLoading(false);
    }
  }, [resolvedOrgId]);

  const respond = useCallback(
    async (id: string, decision: ExecApprovalDecision, comment?: string) => {
      const org = resolvedOrgId.trim();
      if (!org) {
        throw new Error("Missing org_id");
      }

      const url = new URL(
        `${API_BASE}/api/approvals/exec/${encodeURIComponent(id)}/respond`,
        window.location.origin,
      );
      url.searchParams.set("org_id", org);

      const response = await fetch(url.toString(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ decision, comment: comment ?? "" }),
      });

      if (!response.ok) {
        const payload = (await response.json().catch(() => null)) as
          | { error?: string }
          | null;
        throw new Error(payload?.error || "Failed to respond");
      }

      const payload = (await response.json()) as { request?: unknown };
      const parsed = parseExecApproval(payload.request);
      if (!parsed) {
        throw new Error("Invalid server response");
      }

      setRequests((prev) => prev.filter((r) => r.id !== id));
      return parsed;
    },
    [resolvedOrgId],
  );

  // Initial load
  useEffect(() => {
    refresh();
  }, [refresh]);

  // Live updates via WebSocket
  useEffect(() => {
    if (!lastMessage || lastMessage.type === "Unknown") return;

    if (lastMessage.type === "ExecApprovalRequested") {
      const parsed = parseExecApproval(lastMessage.data);
      if (!parsed) return;
      if (resolvedOrgId && parsed.org_id !== resolvedOrgId) return;

      setRequests((prev) => {
        if (prev.some((r) => r.id === parsed.id)) return prev;
        return [parsed, ...prev];
      });
      return;
    }

    if (lastMessage.type === "ExecApprovalResolved") {
      const parsed = parseExecApproval(lastMessage.data);
      const id = parsed?.id;
      if (!id) return;

      setRequests((prev) => prev.filter((r) => r.id !== id));
    }
  }, [lastMessage, resolvedOrgId]);

  return {
    orgId: resolvedOrgId ? resolvedOrgId : null,
    requests,
    loading,
    error,
    refresh,
    respond,
  };
}

