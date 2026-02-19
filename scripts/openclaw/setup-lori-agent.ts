#!/usr/bin/env npx tsx

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const REQUIRED_LORI_ENTRY: Record<string, unknown> = {
  id: "lori",
  name: "Lori",
  model: "anthropic/claude-opus-4-6",
  workspace: "~/.openclaw/workspace-lori",
};

type LoriConfigResult = {
  changed: boolean;
  configPath: string;
};

function parseJSONFile(filePath: string): Record<string, unknown> {
  if (!fs.existsSync(filePath)) {
    return {};
  }
  const raw = fs.readFileSync(filePath, "utf8").trim();
  if (!raw) {
    return {};
  }
  const parsed = JSON.parse(raw) as unknown;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return {};
  }
  return { ...(parsed as Record<string, unknown>) };
}

export function ensureLoriAgentConfig(configPath: string): LoriConfigResult {
  const resolvedPath = path.resolve(configPath);
  const root = parseJSONFile(resolvedPath);
  const agentsRecord = (root.agents && typeof root.agents === "object" && !Array.isArray(root.agents))
    ? { ...(root.agents as Record<string, unknown>) }
    : {};
  const list = Array.isArray(agentsRecord.list)
    ? (agentsRecord.list as unknown[])
      .filter((item) => item && typeof item === "object" && !Array.isArray(item))
      .map((item) => ({ ...(item as Record<string, unknown>) }))
    : [];

  const beforeList = JSON.stringify(list);
  const loriIndex = list.findIndex((entry) => String(entry.id || "").trim().toLowerCase() === "lori");
  if (loriIndex >= 0) {
    const existing = list[loriIndex] || {};
    const merged = {
      ...existing,
      ...REQUIRED_LORI_ENTRY,
    };
    list[loriIndex] = merged;
  } else {
    list.push({ ...REQUIRED_LORI_ENTRY });
  }

  const afterList = JSON.stringify(list);
  const changed = beforeList !== afterList;
  agentsRecord.list = list;
  root.agents = agentsRecord;

  if (changed || !fs.existsSync(resolvedPath)) {
    fs.mkdirSync(path.dirname(resolvedPath), { recursive: true });
    fs.writeFileSync(resolvedPath, `${JSON.stringify(root, null, 2)}\n`, "utf8");
  }

  return {
    changed,
    configPath: resolvedPath,
  };
}

function resolveConfigPathFromCLI(argv: string[]): string {
  const index = argv.findIndex((value) => value === "--config");
  if (index >= 0) {
    const candidate = String(argv[index + 1] || "").trim();
    if (!candidate) {
      throw new Error("--config requires a path value");
    }
    return candidate;
  }
  return path.join(os.homedir(), ".openclaw", "openclaw.json");
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const configPath = resolveConfigPathFromCLI(process.argv.slice(2));
  const result = ensureLoriAgentConfig(configPath);
  const changeLabel = result.changed ? "updated" : "unchanged";
  console.log(`[setup-lori-agent] ${changeLabel}: ${result.configPath}`);
}
