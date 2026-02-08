import { useCallback, useEffect, useMemo, useState } from "react";
import { useWS } from "../contexts/WebSocketContext";

const API_BASE = import.meta.env.VITE_API_URL || "";
const ORG_STORAGE_KEY = "otter-camp-org-id";
const DEFAULT_LIMIT = 25;

export type EmissionScope = {
  project_id?: string;
  issue_id?: string;
  issue_number?: number;
};

export type EmissionProgress = {
  current: number;
  total: number;
  unit?: string;
};

export type Emission = {
  id: string;
  source_type: string;
  source_id: string;
  kind: string;
  summary: string;
  detail?: string;
  timestamp: string;
  scope?: EmissionScope;
  progress?: EmissionProgress;
};

type EmissionListResponse = {
  items?: unknown;
};

type UseEmissionsOptions = {
  orgId?: string;
  projectId?: string;
  issueId?: string;
  sourceId?: string;
  limit?: number;
};

type UseEmissionsResult = {
  emissions: Emission[];
  latestBySource: Map<string, Emission>;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
};

const getTrimmedString = (value: unknown): string => {
  if (typeof value !== "string") {
    return "";
  }
  return value.trim();
};

const getNumber = (value: unknown): number | null => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value.trim());
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return null;
};

const parseEmission = (raw: unknown): Emission | null => {
  if (!raw || typeof raw !== "object") {
    return null;
  }

  const record = raw as Record<string, unknown>;
  const id = getTrimmedString(record.id);
  const sourceType = getTrimmedString(record.source_type);
  const sourceID = getTrimmedString(record.source_id);
  const kind = getTrimmedString(record.kind);
  const summary = getTrimmedString(record.summary);
  const timestamp = getTrimmedString(record.timestamp);

  if (!id || !sourceType || !sourceID || !kind || !summary || !timestamp) {
    return null;
  }
  if (Number.isNaN(new Date(timestamp).getTime())) {
    return null;
  }

  let scope: EmissionScope | undefined;
  if (record.scope && typeof record.scope === "object") {
    const scopeRecord = record.scope as Record<string, unknown>;
    const projectID = getTrimmedString(scopeRecord.project_id);
    const issueID = getTrimmedString(scopeRecord.issue_id);
    const issueNumberValue = getNumber(scopeRecord.issue_number);
    if (projectID || issueID || issueNumberValue !== null) {
      scope = {
        ...(projectID ? { project_id: projectID } : {}),
        ...(issueID ? { issue_id: issueID } : {}),
        ...(issueNumberValue !== null
          ? { issue_number: Math.trunc(issueNumberValue) }
          : {}),
      };
    }
  }

  let progress: EmissionProgress | undefined;
  if (record.progress && typeof record.progress === "object") {
    const progressRecord = record.progress as Record<string, unknown>;
    const current = getNumber(progressRecord.current);
    const total = getNumber(progressRecord.total);
    const unit = getTrimmedString(progressRecord.unit);
    if (
      current !== null &&
      total !== null &&
      current >= 0 &&
      total > 0 &&
      current <= total
    ) {
      progress = {
        current: Math.trunc(current),
        total: Math.trunc(total),
        ...(unit ? { unit } : {}),
      };
    }
  }

  const detail = getTrimmedString(record.detail);

  return {
    id,
    source_type: sourceType,
    source_id: sourceID,
    kind,
    summary,
    timestamp: new Date(timestamp).toISOString(),
    ...(detail ? { detail } : {}),
    ...(scope ? { scope } : {}),
    ...(progress ? { progress } : {}),
  };
};

const resolveOrgID = (providedOrgID?: string): string => {
  const explicit = (providedOrgID ?? "").trim();
  if (explicit) {
    return explicit;
  }
  try {
    return (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
};

const emissionMatchesFilters = (
  emission: Emission,
  filter: { projectID: string; issueID: string; sourceID: string },
): boolean => {
  if (filter.sourceID && emission.source_id !== filter.sourceID) {
    return false;
  }
  if (filter.projectID && emission.scope?.project_id !== filter.projectID) {
    return false;
  }
  if (filter.issueID && emission.scope?.issue_id !== filter.issueID) {
    return false;
  }
  return true;
};

const parseWSMessageEmission = (data: unknown): Emission | null => {
  const parsed = parseEmission(data);
  if (parsed) {
    return parsed;
  }
  if (!data || typeof data !== "object") {
    return null;
  }
  const record = data as Record<string, unknown>;
  return parseEmission(record.emission);
};

export default function useEmissions(
  options: UseEmissionsOptions = {},
): UseEmissionsResult {
  const { connected, lastMessage, sendMessage } = useWS();
  const orgID = useMemo(() => resolveOrgID(options.orgId), [options.orgId]);
  const projectID = (options.projectId ?? "").trim();
  const issueID = (options.issueId ?? "").trim();
  const sourceID = (options.sourceId ?? "").trim();
  const limit = Number.isFinite(options.limit) && (options.limit ?? 0) > 0
    ? Math.trunc(options.limit as number)
    : DEFAULT_LIMIT;

  const [emissions, setEmissions] = useState<Emission[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!orgID) {
      setEmissions([]);
      setLoading(false);
      setError(null);
      return;
    }

    setLoading(true);
    setError(null);
    try {
      const url = new URL(`${API_BASE}/api/emissions/recent`, window.location.origin);
      url.searchParams.set("org_id", orgID);
      url.searchParams.set("limit", String(limit));
      if (projectID) {
        url.searchParams.set("project_id", projectID);
      }
      if (issueID) {
        url.searchParams.set("issue_id", issueID);
      }
      if (sourceID) {
        url.searchParams.set("source_id", sourceID);
      }

      const response = await fetch(url.toString(), {
        headers: { "Content-Type": "application/json" },
      });
      if (!response.ok) {
        const payload = (await response.json().catch(() => null)) as
          | { error?: string }
          | null;
        throw new Error(payload?.error || "Failed to load emissions");
      }

      const payload = (await response.json()) as EmissionListResponse;
      const parsed = Array.isArray(payload.items)
        ? (payload.items as unknown[])
            .map(parseEmission)
            .filter((item): item is Emission => item !== null)
        : [];
      setEmissions(parsed.slice(0, limit));
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to load emissions";
      setError(message);
    } finally {
      setLoading(false);
    }
  }, [issueID, limit, orgID, projectID, sourceID]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!connected) {
      return;
    }
    const topics: string[] = [];
    if (projectID) {
      topics.push(`project:${projectID}`);
    }
    if (issueID) {
      topics.push(`issue:${issueID}`);
    }
    for (const topic of topics) {
      sendMessage({ type: "subscribe", topic });
    }

    return () => {
      for (const topic of topics) {
        sendMessage({ type: "unsubscribe", topic });
      }
    };
  }, [connected, issueID, projectID, sendMessage]);

  useEffect(() => {
    if (!lastMessage || lastMessage.type !== "EmissionReceived") {
      return;
    }
    const incoming = parseWSMessageEmission(lastMessage.data);
    if (!incoming) {
      return;
    }
    if (
      !emissionMatchesFilters(incoming, {
        projectID,
        issueID,
        sourceID,
      })
    ) {
      return;
    }

    setEmissions((prev) => {
      const withoutExisting = prev.filter((item) => item.id !== incoming.id);
      return [incoming, ...withoutExisting].slice(0, limit);
    });
  }, [issueID, lastMessage, limit, projectID, sourceID]);

  const latestBySource = useMemo(() => {
    const bySource = new Map<string, Emission>();
    for (const emission of emissions) {
      if (!bySource.has(emission.source_id)) {
        bySource.set(emission.source_id, emission);
      }
    }
    return bySource;
  }, [emissions]);

  return {
    emissions,
    latestBySource,
    loading,
    error,
    refresh,
  };
}
