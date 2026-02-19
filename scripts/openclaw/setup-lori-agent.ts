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
const REQUIRED_LORI_FILES = ["SOUL.md", "IDENTITY.md", "TOOLS.md"] as const;
const LORI_FILE_TEMPLATES: Record<(typeof REQUIRED_LORI_FILES)[number], string> = {
  "SOUL.md": [
    "# Lori Soul",
    "",
    "Lori focuses on people-management and interpersonal coordination in OtterCamp.",
    "",
  ].join("\n"),
  "IDENTITY.md": [
    "# Lori Identity",
    "",
    "- Agent ID: lori",
    "- Role: People management and relationship support",
    "",
  ].join("\n"),
  "TOOLS.md": [
    "# Lori Tools",
    "",
    "- Use standard OpenClaw/OtterCamp toolchain via identity injection.",
    "",
  ].join("\n"),
};

type LoriConfigResult = {
  changed: boolean;
  configPath: string;
};

type LoriWorkspaceResult = {
  changed: boolean;
  workspacePath: string;
  samsBrainPath: string;
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

export function ensureLoriWorkspaceScaffold(workspacePath: string, samsBrainPath: string): LoriWorkspaceResult {
  const resolvedWorkspace = path.resolve(workspacePath);
  const resolvedSamsBrain = path.resolve(samsBrainPath);
  let changed = false;

  if (!fs.existsSync(resolvedWorkspace)) {
    fs.mkdirSync(resolvedWorkspace, { recursive: true });
    changed = true;
  }
  if (!fs.existsSync(resolvedSamsBrain)) {
    fs.mkdirSync(resolvedSamsBrain, { recursive: true });
    changed = true;
  }

  for (const fileName of REQUIRED_LORI_FILES) {
    const samsBrainFile = path.join(resolvedSamsBrain, fileName);
    const workspaceFile = path.join(resolvedWorkspace, fileName);
    if (!fs.existsSync(samsBrainFile)) {
      fs.writeFileSync(samsBrainFile, LORI_FILE_TEMPLATES[fileName], "utf8");
      changed = true;
    }

    const expectedTarget = fs.realpathSync(samsBrainFile);
    if (fs.existsSync(workspaceFile)) {
      const stat = fs.lstatSync(workspaceFile);
      if (stat.isSymbolicLink()) {
        const linkedTarget = fs.realpathSync(workspaceFile);
        if (linkedTarget !== expectedTarget) {
          fs.rmSync(workspaceFile, { force: true });
          fs.symlinkSync(samsBrainFile, workspaceFile);
          changed = true;
        }
      } else {
        fs.rmSync(workspaceFile, { recursive: true, force: true });
        fs.symlinkSync(samsBrainFile, workspaceFile);
        changed = true;
      }
    } else {
      fs.symlinkSync(samsBrainFile, workspaceFile);
      changed = true;
    }
  }

  return {
    changed,
    workspacePath: resolvedWorkspace,
    samsBrainPath: resolvedSamsBrain,
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
