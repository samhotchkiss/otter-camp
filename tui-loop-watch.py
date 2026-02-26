#!/usr/bin/env python3
"""
tui-loop-watch.py — Live dashboard for the TUI UX testing Ralph loop.

Shows:
  • Functionality coverage progress (per section + totals)
  • Experience improvements funnel (Discovered → Filed → Implemented → Tested)
  • Issues queue (144+ range, all lanes)
  • Loop health (iteration, server/worker status)
  • Recent activity feed

Usage:
  python3 tui-loop-watch.py [--repo PATH] [--interval SECS] [--no-color] [--hype]
"""

import argparse
import collections
import glob
import os
import re
import select
import shutil
import signal
import subprocess
import sys
import termios
import time
import tty
import urllib.request
import urllib.error
from dataclasses import dataclass, field
from typing import Deque, Dict, List, Optional, Tuple

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

REPO_DEFAULT = os.path.dirname(os.path.abspath(__file__))
ISSUES_DIR_NAME = "issues"
PROGRESS_FILE = "tui-ux-loop-progress.md"
EXPERIENCE_FILE = "experience.md"
FUNC_LIST_FILE = "functionality-list.md"
SERVER_URL = "http://localhost:4110/health/live"
WORKER_LOG = "/tmp/ottercamp-worker.log"
SERVER_LOG = "/tmp/ottercamp-server.log"
MIN_ISSUE_NUM = 144  # only track issues >= this number

LANES = ["01-ready", "02-in-progress", "03-needs-review", "04-in-review", "05-completed"]
LANE_LABELS = {
    "01-ready": "Ready",
    "02-in-progress": "In Progress",
    "03-needs-review": "Needs Review",
    "04-in-review": "In Review",
    "05-completed": "Done",
}
LANE_COLORS = {
    "01-ready": "38;5;45",
    "02-in-progress": "38;5;220",
    "03-needs-review": "38;5;208",
    "04-in-review": "38;5;201",
    "05-completed": "38;5;82",
}

# ANSI helpers
RESET = "\033[0m"
BOLD = "\033[1m"
DIM = "\033[2m"

# Checkbox states in functionality-list.md
# [x] pass  [~] partial  [!] broken/issue  [-] n/a  [ ] untested
RE_CHECK = re.compile(r"^- \[(.)\]")
RE_SECTION = re.compile(r"^## (\d+)\. (.+)$")

# Experience improvement statuses parsed from experience.md
RE_EX_ENTRY = re.compile(r"^## EX-(\d+): (.+)$")
RE_EX_STATUS = re.compile(r"\*\*Status:\*\* (.+)$")
RE_EX_ISSUE = re.compile(r"\*\*Issue:\*\* #(\d+)")

# Issue file naming
RE_ISSUE_NUM = re.compile(r"^(\d+)-")

# Progress file
RE_ITER = re.compile(r"^Current iteration:\s*(\d+)")
RE_NEXT_ISSUE = re.compile(r"^Next issue number:\s*(\d+)")
RE_CURRENT_SECTION = re.compile(r"^Currently testing:\s*(.+)$")
RE_EX_TESTED_COUNT = re.compile(r"^Tested \(counts toward 20\):\s*(\d+)")

# Dance frames
OTTER_DANCE = [r"(>'-')>", r"<('-'<)", r"^('-')^", r"v('-')v"]
SPINNER = ["|", "/", "-", "\\"]
SPARK_CHARS = " .:-=+*#%@"

KEY_UP = "UP"
KEY_DOWN = "DOWN"
KEY_LEFT = "LEFT"
KEY_RIGHT = "RIGHT"

# Cache
_func_cache: Dict[str, Tuple[float, object]] = {}
_exp_cache: Dict[str, Tuple[float, object]] = {}
_prog_cache: Dict[str, Tuple[float, object]] = {}


# ---------------------------------------------------------------------------
# Data structures
# ---------------------------------------------------------------------------

@dataclass
class SectionProgress:
    num: int
    title: str
    total: int = 0
    passed: int = 0      # [x]
    partial: int = 0     # [~]
    broken: int = 0      # [!]
    skipped: int = 0     # [-]
    untested: int = 0    # [ ]


@dataclass
class ExEntry:
    num: int
    title: str
    status: str = "Discovered"   # Discovered | Filed | Implemented | Tested
    issue_num: Optional[int] = None


@dataclass
class LoopProgress:
    iteration: int = 0
    next_issue: int = MIN_ISSUE_NUM
    current_section: str = ""
    ex_tested: int = 0
    last_mtime: float = 0.0


@dataclass
class IssueQueue:
    lanes: Dict[str, List[str]] = field(default_factory=dict)


@dataclass
class ServerStatus:
    up: bool = False
    latency_ms: int = 0


@dataclass
class Snapshot:
    sections: List[SectionProgress]
    ex_entries: List[ExEntry]
    progress: LoopProgress
    queue: IssueQueue
    server: ServerStatus
    worker_age_s: Optional[int]
    server_age_s: Optional[int]


# ---------------------------------------------------------------------------
# Input reader (same pattern as queue-dance.py)
# ---------------------------------------------------------------------------

class InputReader:
    def __init__(self):
        self.fd = None
        self.old = None

    def __enter__(self):
        if sys.stdin.isatty():
            self.fd = sys.stdin.fileno()
            self.old = termios.tcgetattr(self.fd)
            tty.setcbreak(self.fd)
        return self

    def __exit__(self, *_):
        if self.fd is not None and self.old is not None:
            termios.tcsetattr(self.fd, termios.TCSADRAIN, self.old)

    def read_key(self) -> Optional[str]:
        if self.fd is None:
            return None
        r, _, _ = select.select([self.fd], [], [], 0)
        if not r:
            return None
        try:
            ch = os.read(self.fd, 1)
            if not ch:
                return None
            if ch == b"\x1b":
                r2, _, _ = select.select([self.fd], [], [], 0.003)
                if not r2:
                    return None
                ch2 = os.read(self.fd, 1)
                if ch2 == b"[":
                    r3, _, _ = select.select([self.fd], [], [], 0.003)
                    if not r3:
                        return None
                    ch3 = os.read(self.fd, 1)
                    if ch3 == b"A":
                        return KEY_UP
                    if ch3 == b"B":
                        return KEY_DOWN
                    if ch3 == b"C":
                        return KEY_RIGHT
                    if ch3 == b"D":
                        return KEY_LEFT
                return None
            key = ch.decode("utf-8", errors="ignore")
            if key in ("k", "K"):
                return KEY_UP
            if key in ("j", "J"):
                return KEY_DOWN
            if key in ("h", "H"):
                return KEY_LEFT
            if key in ("l", "L"):
                return KEY_RIGHT
            return key
        except OSError:
            return None


# ---------------------------------------------------------------------------
# ANSI helpers
# ---------------------------------------------------------------------------

def ansi(code: str) -> str:
    return f"\033[{code}m"

def clr(text: str, code: str, enabled: bool) -> str:
    if not enabled:
        return text
    return f"{ansi(code)}{text}{RESET}"

def truncate(text: str, width: int) -> str:
    if width <= 0:
        return ""
    if len(text) <= width:
        return text
    return text[: width - 1] + "…"

def pad(text: str, width: int) -> str:
    clipped = truncate(text, width)
    return clipped + " " * max(0, width - len(clipped))

def bar(filled: int, total: int, width: int, color_code: str = "38;5;82", enabled: bool = True) -> str:
    if total == 0:
        pct = 0.0
    else:
        pct = filled / total
    fill = int(width * pct)
    b = "█" * fill + "░" * (width - fill)
    return clr(b, color_code, enabled)

def file_age_s(path: str) -> Optional[int]:
    try:
        return int(time.time() - os.path.getmtime(path))
    except OSError:
        return None


# ---------------------------------------------------------------------------
# Data loaders
# ---------------------------------------------------------------------------

def load_functionality(path: str) -> List[SectionProgress]:
    try:
        mtime = os.path.getmtime(path)
    except OSError:
        return []
    cached = _func_cache.get(path)
    if cached and cached[0] == mtime:
        return cached[1]  # type: ignore

    sections: List[SectionProgress] = []
    current: Optional[SectionProgress] = None

    try:
        with open(path, encoding="utf-8", errors="ignore") as f:
            for line in f:
                sec_m = RE_SECTION.match(line.rstrip())
                if sec_m:
                    current = SectionProgress(num=int(sec_m.group(1)), title=sec_m.group(2))
                    sections.append(current)
                    continue
                if current is None:
                    continue
                chk_m = RE_CHECK.match(line)
                if chk_m:
                    state = chk_m.group(1)
                    current.total += 1
                    if state == "x":
                        current.passed += 1
                    elif state == "~":
                        current.partial += 1
                    elif state == "!":
                        current.broken += 1
                    elif state == "-":
                        current.skipped += 1
                    else:
                        current.untested += 1
    except OSError:
        pass

    _func_cache[path] = (mtime, sections)
    return sections


def load_experience(path: str) -> List[ExEntry]:
    try:
        mtime = os.path.getmtime(path)
    except OSError:
        return []
    cached = _exp_cache.get(path)
    if cached and cached[0] == mtime:
        return cached[1]  # type: ignore

    entries: List[ExEntry] = []
    current: Optional[ExEntry] = None

    try:
        with open(path, encoding="utf-8", errors="ignore") as f:
            for line in f:
                stripped = line.rstrip()
                ex_m = RE_EX_ENTRY.match(stripped)
                if ex_m:
                    current = ExEntry(num=int(ex_m.group(1)), title=ex_m.group(2))
                    entries.append(current)
                    continue
                if current is None:
                    continue
                st_m = RE_EX_STATUS.search(stripped)
                if st_m:
                    raw = st_m.group(1)
                    # Parse checkbox pattern like: [x] Discovered | [x] Filed | [ ] Implemented | [ ] Tested
                    # Or plain text like: Tested
                    # Determine highest completed stage
                    stages = ["Tested", "Implemented", "Filed", "Discovered"]
                    found = "Discovered"
                    for stage in stages:
                        # Check for [x] Stage pattern
                        if re.search(r"\[x\]\s*" + stage, raw, re.IGNORECASE):
                            found = stage
                            break
                        # Or plain text match
                        if stage.lower() in raw.lower():
                            found = stage
                            break
                    current.status = found
                    continue
                iss_m = RE_EX_ISSUE.search(stripped)
                if iss_m and current:
                    current.issue_num = int(iss_m.group(1))
    except OSError:
        pass

    _exp_cache[path] = (mtime, entries)
    return entries


def load_progress(path: str) -> LoopProgress:
    prog = LoopProgress()
    try:
        mtime = os.path.getmtime(path)
        prog.last_mtime = mtime
    except OSError:
        return prog
    cached = _prog_cache.get(path)
    if cached and cached[0] == mtime:
        return cached[1]  # type: ignore

    try:
        with open(path, encoding="utf-8", errors="ignore") as f:
            for line in f:
                stripped = line.strip()
                m = RE_ITER.match(stripped)
                if m:
                    prog.iteration = int(m.group(1))
                    continue
                m = RE_NEXT_ISSUE.match(stripped)
                if m:
                    prog.next_issue = int(m.group(1))
                    continue
                m = RE_CURRENT_SECTION.match(stripped)
                if m:
                    prog.current_section = m.group(1)
                    continue
                m = RE_EX_TESTED_COUNT.match(stripped)
                if m:
                    prog.ex_tested = int(m.group(1))
    except OSError:
        pass

    _prog_cache[path] = (mtime, prog)
    return prog


def load_issue_queue(issues_dir: str) -> IssueQueue:
    q = IssueQueue(lanes={lane: [] for lane in LANES})
    for lane in LANES:
        d = os.path.join(issues_dir, lane)
        if not os.path.isdir(d):
            continue
        files = sorted(f for f in os.listdir(d) if f.endswith(".md"))
        filtered = []
        for f in files:
            m = RE_ISSUE_NUM.match(f)
            if m and int(m.group(1)) >= MIN_ISSUE_NUM:
                filtered.append(f)
        q.lanes[lane] = filtered
    return q


def check_server(url: str) -> ServerStatus:
    try:
        t0 = time.monotonic()
        req = urllib.request.Request(url, method="GET")
        with urllib.request.urlopen(req, timeout=1.5) as resp:
            _ = resp.read()
            latency = int((time.monotonic() - t0) * 1000)
            return ServerStatus(up=resp.status < 400, latency_ms=latency)
    except Exception:
        return ServerStatus(up=False, latency_ms=0)


def load_snapshot(repo: str) -> Snapshot:
    issues_dir = os.path.join(repo, ISSUES_DIR_NAME)
    func_path = os.path.join(issues_dir, FUNC_LIST_FILE)
    exp_path = os.path.join(issues_dir, EXPERIENCE_FILE)
    prog_path = os.path.join(issues_dir, PROGRESS_FILE)

    sections = load_functionality(func_path)
    ex_entries = load_experience(exp_path)
    progress = load_progress(prog_path)
    queue = load_issue_queue(issues_dir)
    server = check_server(SERVER_URL)
    worker_age = file_age_s(WORKER_LOG)
    server_age = file_age_s(SERVER_LOG)

    return Snapshot(
        sections=sections,
        ex_entries=ex_entries,
        progress=progress,
        queue=queue,
        server=server,
        worker_age_s=worker_age,
        server_age_s=server_age,
    )


# ---------------------------------------------------------------------------
# Event detection
# ---------------------------------------------------------------------------

def detect_events(prev: Optional[Snapshot], curr: Snapshot, events: Deque[str]) -> int:
    if prev is None:
        return 0

    added = 0
    ts = time.strftime("%H:%M:%S")

    # Issue queue movements
    prev_locs: Dict[str, str] = {}
    curr_locs: Dict[str, str] = {}
    for lane, files in (prev.queue.lanes or {}).items():
        for f in files:
            prev_locs[f] = lane
    for lane, files in (curr.queue.lanes or {}).items():
        for f in files:
            curr_locs[f] = lane

    for f, lane in curr_locs.items():
        if f not in prev_locs:
            events.appendleft(f"{ts}  + issue {f[:-3]} entered {LANE_LABELS[lane]}")
            added += 1
        elif prev_locs[f] != lane:
            events.appendleft(f"{ts}  → {f[:-3]}: {LANE_LABELS[prev_locs[f]]} → {LANE_LABELS[lane]}")
            added += 1

    # EX improvement status changes
    prev_ex = {e.num: e.status for e in prev.ex_entries}
    for e in curr.ex_entries:
        old = prev_ex.get(e.num)
        if old is None:
            events.appendleft(f"{ts}  ✦ EX-{e.num:03d} discovered: {truncate(e.title, 40)}")
            added += 1
        elif old != e.status:
            events.appendleft(f"{ts}  ✦ EX-{e.num:03d} {old} → {e.status}")
            added += 1

    # Functionality changes
    prev_sec = {s.num: s.passed + s.partial + s.broken for s in prev.sections}
    for s in curr.sections:
        prev_done = prev_sec.get(s.num, 0)
        curr_done = s.passed + s.partial + s.broken
        if curr_done > prev_done:
            delta = curr_done - prev_done
            events.appendleft(f"{ts}  ✓ §{s.num} {truncate(s.title, 30)}: +{delta} items tested")
            added += 1

    return added


# ---------------------------------------------------------------------------
# Rendering
# ---------------------------------------------------------------------------

VIEW_OVERVIEW = 0
VIEW_SECTIONS = 1
VIEW_EXPERIENCE = 2
VIEW_ISSUES = 3

VIEW_NAMES = ["Overview", "Sections", "Experience", "Issues"]


@dataclass
class UIState:
    view: int = VIEW_OVERVIEW
    scroll: int = 0


def render_status_line(snap: Snapshot, frame: int, color_enabled: bool, cols: int) -> str:
    # Server dot
    if snap.server.up:
        srv = clr("● UP", "38;5;82", color_enabled) + clr(f" {snap.server.latency_ms}ms", "38;5;244", color_enabled)
    else:
        srv = clr("● DOWN", "38;5;196", color_enabled)

    # Worker
    age = snap.worker_age_s
    if age is None:
        wkr = clr("● ?", "38;5;244", color_enabled)
    elif age < 30:
        wkr = clr(f"● {age}s", "38;5;82", color_enabled)
    elif age < 120:
        wkr = clr(f"● {age}s", "38;5;220", color_enabled)
    else:
        wkr = clr(f"● {age}s idle", "38;5;196", color_enabled)

    itr = snap.progress.iteration
    itr_str = clr(f"iter #{itr}", "38;5;45", color_enabled) if itr else clr("iter ?", "38;5;244", color_enabled)

    sp = SPINNER[frame % 4]
    parts = [
        f"{BOLD}TUI UX LOOP{RESET}  {sp}",
        itr_str,
        f"server: {srv}",
        f"worker: {wkr}",
        time.strftime("%H:%M:%S"),
    ]
    return "  |  ".join(parts)


def render_ex_funnel(ex_entries: List[ExEntry], color_enabled: bool, cols: int) -> List[str]:
    counts = {"Discovered": 0, "Filed": 0, "Implemented": 0, "Tested": 0}
    for e in ex_entries:
        s = e.status
        if s in counts:
            counts[s] += 1
        # Anything above "Discovered" also counts at lower levels
        order = ["Discovered", "Filed", "Implemented", "Tested"]
        if s in order:
            idx = order.index(s)
            for lower in order[:idx]:
                counts[lower] += 1  # already counted above—just for display

    # Recount cleanly: each stage is count AT THAT STAGE OR HIGHER
    by_stage = {"Discovered": 0, "Filed": 0, "Implemented": 0, "Tested": 0}
    for e in ex_entries:
        order = ["Discovered", "Filed", "Implemented", "Tested"]
        if e.status in order:
            i = order.index(e.status)
            for stage in order[: i + 1]:
                by_stage[stage] += 1

    goal = 20
    tested = by_stage["Tested"]
    pct_bar = bar(tested, goal, 20, "38;5;82", color_enabled)

    lines = []
    lines.append(clr("EXPERIENCE IMPROVEMENTS", "1;38;5;51", color_enabled))
    lines.append(
        f"  Goal: {tested}/{goal} Tested  {pct_bar}  "
        + (clr("✓ COMPLETE", "38;5;82", color_enabled) if tested >= goal else clr(f"{goal - tested} remaining", "38;5;220", color_enabled))
    )
    lines.append("")

    stage_colors = {
        "Discovered": "38;5;244",
        "Filed": "38;5;45",
        "Implemented": "38;5;220",
        "Tested": "38;5;82",
    }
    stage_icons = {
        "Discovered": "○",
        "Filed": "◎",
        "Implemented": "◉",
        "Tested": "✓",
    }
    for stage in ["Discovered", "Filed", "Implemented", "Tested"]:
        n = by_stage[stage]
        icon = stage_icons[stage]
        line = f"  {clr(icon, stage_colors[stage], color_enabled)} {clr(stage, stage_colors[stage], color_enabled)}: {n}"
        lines.append(line)

    return lines


def render_func_summary(sections: List[SectionProgress], color_enabled: bool, cols: int) -> List[str]:
    total_items = sum(s.total for s in sections)
    total_done = sum(s.passed + s.partial + s.broken + s.skipped for s in sections)
    total_pass = sum(s.passed for s in sections)
    total_broken = sum(s.broken for s in sections)
    total_skip = sum(s.skipped for s in sections)
    total_untested = sum(s.untested for s in sections)

    lines = []
    lines.append(clr("FUNCTIONALITY COVERAGE", "1;38;5;51", color_enabled))

    pct_bar = bar(total_done, total_items, 24, "38;5;82", color_enabled)
    lines.append(
        f"  {pct_bar}  {total_done}/{total_items} evaluated  "
        + clr(f"+{total_pass} pass", "38;5;82", color_enabled) + "  "
        + (clr(f"!{total_broken} broken", "38;5;196", color_enabled) if total_broken else clr("!0 broken", "38;5;244", color_enabled)) + "  "
        + clr(f"~{total_untested} untested", "38;5;244", color_enabled)
    )
    lines.append("")
    return lines


def render_sections_list(sections: List[SectionProgress], color_enabled: bool, cols: int, scroll: int) -> List[str]:
    lines = []
    lines.append(clr("SECTION-BY-SECTION PROGRESS", "1;38;5;51", color_enabled))
    lines.append(clr(f"  {'#':<4} {'Section':<32} {'Pass':>5} {'~':>4} {'!':>4} {'-':>4} {'?':>5} {'Total':>6}", "38;5;244", color_enabled))
    lines.append(clr("  " + "─" * min(cols - 4, 70), "38;5;237", color_enabled))

    visible = sections[scroll:]
    for s in visible:
        done = s.passed + s.partial + s.broken + s.skipped
        pct = done / s.total if s.total else 0.0
        if pct >= 1.0:
            sec_color = "38;5;82"
            icon = "✓"
        elif pct > 0:
            sec_color = "38;5;220"
            icon = "●"
        else:
            sec_color = "38;5;244"
            icon = "○"

        title = truncate(s.title, 32)
        b_str = f"[{'█' * int(pct * 8)}{'░' * (8 - int(pct * 8))}]"
        pass_str = clr(str(s.passed), "38;5;82", color_enabled) if s.passed else clr("0", "38;5;237", color_enabled)
        part_str = clr(str(s.partial), "38;5;220", color_enabled) if s.partial else clr("0", "38;5;237", color_enabled)
        brk_str = clr(str(s.broken), "38;5;196", color_enabled) if s.broken else clr("0", "38;5;237", color_enabled)
        skip_str = clr(str(s.skipped), "38;5;244", color_enabled)
        unt_str = clr(str(s.untested), "38;5;244", color_enabled)

        line = (
            f"  {clr(icon, sec_color, color_enabled)} "
            f"{clr(f'{s.num:<3}', sec_color, color_enabled)} "
            f"{pad(title, 32)} "
            f"{clr(b_str, sec_color, color_enabled)} "
            f"{pass_str:>5} "
            f"{part_str:>4} "
            f"{brk_str:>4} "
            f"{skip_str:>4} "
            f"{unt_str:>5} "
            f"{clr(str(s.total), '38;5;244', color_enabled):>6}"
        )
        lines.append(line)

    return lines


def render_experience_list(ex_entries: List[ExEntry], color_enabled: bool, cols: int, scroll: int) -> List[str]:
    lines = []
    lines.append(clr("EXPERIENCE IMPROVEMENTS — FULL LIST", "1;38;5;51", color_enabled))

    stage_colors = {"Discovered": "38;5;244", "Filed": "38;5;45", "Implemented": "38;5;220", "Tested": "38;5;82"}
    stage_icons = {"Discovered": "○", "Filed": "◎", "Implemented": "◉", "Tested": "✓"}

    if not ex_entries:
        lines.append(clr("  (none yet — start testing to discover improvements)", "38;5;244", color_enabled))
        return lines

    visible = ex_entries[scroll:]
    for e in visible:
        sc = stage_colors.get(e.status, "38;5;244")
        icon = stage_icons.get(e.status, "○")
        iss = f" #{e.issue_num}" if e.issue_num else ""
        line = (
            f"  {clr(icon, sc, color_enabled)} "
            f"{clr(f'EX-{e.num:03d}', sc, color_enabled)} "
            f"{clr(f'[{e.status}]', sc, color_enabled)}"
            f"{clr(iss, '38;5;244', color_enabled)}  "
            f"{truncate(e.title, cols - 30)}"
        )
        lines.append(line)

    return lines


def render_issue_queue(queue: IssueQueue, color_enabled: bool, cols: int) -> List[str]:
    lines = []
    lines.append(clr("ISSUE QUEUE  (≥144)", "1;38;5;51", color_enabled))

    # Summary bar
    counts = {lane: len(queue.lanes.get(lane, [])) for lane in LANES}
    total = sum(counts.values())
    parts = []
    for lane in LANES:
        c = counts[lane]
        label = LANE_LABELS[lane]
        col = LANE_COLORS[lane]
        parts.append(clr(f"{label} ({c})", col, color_enabled))
    lines.append("  " + "  │  ".join(parts))
    lines.append("")

    # Files in each lane
    for lane in LANES:
        files = queue.lanes.get(lane, [])
        if not files:
            continue
        col = LANE_COLORS[lane]
        lines.append(clr(f"  {LANE_LABELS[lane]}:", col, color_enabled))
        for f in files[:5]:
            name = f[:-3]  # strip .md
            lines.append(f"    {clr('·', col, color_enabled)} {name}")
        if len(files) > 5:
            lines.append(clr(f"    … and {len(files) - 5} more", "38;5;244", color_enabled))

    if total == 0:
        lines.append(clr("  (no loop issues filed yet)", "38;5;244", color_enabled))

    return lines


def render_events(events: Deque[str], color_enabled: bool, cols: int, n: int = 6) -> List[str]:
    lines = []
    lines.append(clr("RECENT ACTIVITY", "1;38;5;51", color_enabled))
    if not events:
        lines.append(clr("  (waiting for first activity…)", "38;5;244", color_enabled))
    else:
        for e in list(events)[:n]:
            lines.append(clr(f"  {truncate(e, cols - 4)}", "38;5;250", color_enabled))
    return lines


def render_overview(snap: Snapshot, frame: int, color_enabled: bool, cols: int, rows: int) -> None:
    """Compact all-up overview — fits in a normal terminal."""
    sections = snap.sections
    ex_entries = snap.ex_entries
    progress = snap.progress

    # --- header row ---
    print(render_status_line(snap, frame, color_enabled, cols))
    print(clr("─" * min(cols, 120), "38;5;237", color_enabled))

    # --- current task ---
    if progress.current_section:
        print(clr(f"  Currently testing: {truncate(progress.current_section, cols - 22)}", "38;5;51", color_enabled))
    else:
        print(clr("  No active section recorded in progress file", "38;5;244", color_enabled))
    print()

    # --- two-column layout: left = funcs, right = EX ---
    left_w = max(30, cols // 2 - 2)
    right_w = cols - left_w - 3

    # Left: functionality coverage
    total_items = sum(s.total for s in sections)
    total_pass = sum(s.passed for s in sections)
    total_partial = sum(s.partial for s in sections)
    total_broken = sum(s.broken for s in sections)
    total_skip = sum(s.skipped for s in sections)
    total_untested = sum(s.untested for s in sections)
    total_done = total_pass + total_partial + total_broken + total_skip

    pct_bar_w = max(10, left_w - 20)
    func_bar = bar(total_done, total_items, pct_bar_w, "38;5;82", color_enabled)
    func_pct = int(100 * total_done / total_items) if total_items else 0

    left_lines = [
        clr("FUNCTIONALITY", "1;38;5;51", color_enabled),
        f"  {func_bar} {func_pct}%",
        f"  {clr(f'✓ {total_pass} pass', '38;5;82', color_enabled)}  "
        f"{clr(f'~ {total_partial} partial', '38;5;220', color_enabled)}  "
        f"{clr(f'! {total_broken} broken', '38;5;196' if total_broken else '38;5;237', color_enabled)}",
        f"  {clr(f'- {total_skip} n/a', '38;5;244', color_enabled)}  "
        f"{clr(f'? {total_untested} untested', '38;5;244', color_enabled)}  "
        f"{clr(f'= {total_items} total', '38;5;244', color_enabled)}",
    ]

    # Right: experience funnel
    goal = 20
    by_stage: Dict[str, int] = {"Discovered": 0, "Filed": 0, "Implemented": 0, "Tested": 0}
    order = ["Discovered", "Filed", "Implemented", "Tested"]
    for e in ex_entries:
        if e.status in order:
            i = order.index(e.status)
            for stage in order[: i + 1]:
                by_stage[stage] += 1

    tested = by_stage["Tested"]
    ex_bar = bar(tested, goal, max(8, right_w - 20), "38;5;82", color_enabled)
    ex_pct = int(100 * tested / goal)
    remaining = goal - tested

    disc_n = by_stage["Discovered"]
    filed_n = by_stage["Filed"]
    impl_n = by_stage["Implemented"]
    right_lines = [
        clr("DELIGHT GOAL", "1;38;5;51", color_enabled),
        f"  {ex_bar} {tested}/{goal} ({ex_pct}%)",
        f"  {clr(f'○ {disc_n} disc', '38;5;244', color_enabled)}  "
        f"{clr(f'◎ {filed_n} filed', '38;5;45', color_enabled)}",
        f"  {clr(f'◉ {impl_n} impl', '38;5;220', color_enabled)}  "
        f"{clr(f'✓ {tested} tested', '38;5;82', color_enabled)}"
        + (clr(f"  ← {remaining} to go", "38;5;196", color_enabled) if remaining > 0 else clr("  ✓ DONE!", "38;5;82", color_enabled)),
    ]

    max_paired = max(len(left_lines), len(right_lines))
    for i in range(max_paired):
        l_text = left_lines[i] if i < len(left_lines) else ""
        r_text = right_lines[i] if i < len(right_lines) else ""
        # Use visible-length truncation (ignoring ANSI codes for padding)
        print(f"{truncate(l_text, left_w):<{left_w}}  │  {r_text}")

    print()

    # --- recent sections progress ---
    active_sections = [s for s in sections if 0 < s.passed + s.partial + s.broken < s.total]
    done_sections = [s for s in sections if s.passed + s.partial + s.broken + s.skipped >= s.total and s.total > 0]
    pending_sections = [s for s in sections if s.passed + s.partial + s.broken == 0 and s.total > 0]

    print(clr("SECTIONS", "1;38;5;51", color_enabled))
    # Show in-progress sections first, then first few pending
    show_secs = active_sections[:3] + pending_sections[:3]
    if not show_secs:
        show_secs = sections[:6]
    for s in show_secs:
        done = s.passed + s.partial + s.broken + s.skipped
        pct = done / s.total if s.total else 0.0
        mini_bar = "█" * int(pct * 10) + "░" * (10 - int(pct * 10))
        if pct >= 1.0:
            col = "38;5;82"
            icon = "✓"
        elif pct > 0:
            col = "38;5;220"
            icon = "●"
        else:
            col = "38;5;244"
            icon = "○"
        print(
            f"  {clr(icon, col, color_enabled)} "
            f"{clr(f'§{s.num}', col, color_enabled)} "
            f"{pad(s.title, 30)} "
            f"{clr(f'[{mini_bar}]', col, color_enabled)} "
            f"{clr(f'{done}/{s.total}', col, color_enabled)}"
        )
    sec_remaining = len(sections) - len(show_secs)
    if sec_remaining > 0:
        print(clr(f"  … {sec_remaining} more sections (press 2 for full list)", "38;5;244", color_enabled))
    done_count = len(done_sections)
    if done_count:
        print(clr(f"  {done_count} sections fully evaluated", "38;5;82", color_enabled))
    print()

    # --- issue queue mini ---
    total_queue = sum(len(v) for v in snap.queue.lanes.values())
    if total_queue:
        parts = []
        for lane in LANES:
            c = len(snap.queue.lanes.get(lane, []))
            if c:
                parts.append(clr(f"{LANE_LABELS[lane]}: {c}", LANE_COLORS[lane], color_enabled))
        print(clr("ISSUES (≥144)  ", "1;38;5;51", color_enabled) + "  ".join(parts))
    else:
        print(clr("ISSUES (≥144)  none filed yet", "38;5;244", color_enabled))
    print()


def render(
    repo: str,
    snap: Snapshot,
    events: Deque[str],
    frame: int,
    color_enabled: bool,
    hype: bool,
    ui: UIState,
) -> None:
    cols, rows = shutil.get_terminal_size((140, 42))
    os.write(sys.stdout.fileno(), b"\033[?25l\033[2J\033[H")

    # Tab bar
    tab_parts = []
    for i, name in enumerate(VIEW_NAMES):
        if i == ui.view:
            tab_parts.append(clr(f" [{name}] ", "1;30;48;5;51", color_enabled))
        else:
            tab_parts.append(clr(f"  {name}  ", "38;5;244", color_enabled))
    tab_bar = "  ".join(tab_parts)
    hint = clr("  1-4: switch tab  |  ↑↓/jk: scroll  |  q: quit", DIM[2:], color_enabled)
    print(tab_bar + hint)
    print(clr("═" * min(cols, 140), "38;5;237", color_enabled))

    if hype:
        otter = OTTER_DANCE[frame % len(OTTER_DANCE)]
        print(clr(f"  {otter}  OtterCamp TUI UX Loop — Live Dashboard", "38;5;51", color_enabled))

    if ui.view == VIEW_OVERVIEW:
        render_overview(snap, frame, color_enabled, cols, rows)
        # events at bottom
        for line in render_events(events, color_enabled, cols, n=5):
            print(line)

    elif ui.view == VIEW_SECTIONS:
        for line in render_func_summary(snap.sections, color_enabled, cols):
            print(line)
        for line in render_sections_list(snap.sections, color_enabled, cols, ui.scroll):
            print(line)

    elif ui.view == VIEW_EXPERIENCE:
        for line in render_ex_funnel(snap.ex_entries, color_enabled, cols):
            print(line)
        print()
        for line in render_experience_list(snap.ex_entries, color_enabled, cols, ui.scroll):
            print(line)

    elif ui.view == VIEW_ISSUES:
        for line in render_issue_queue(snap.queue, color_enabled, cols):
            print(line)
        print()
        for line in render_events(events, color_enabled, cols, n=8):
            print(line)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    parser = argparse.ArgumentParser(description="TUI UX Loop live dashboard")
    parser.add_argument("--repo", default=REPO_DEFAULT)
    parser.add_argument("--interval", type=float, default=2.0)
    parser.add_argument("--no-color", action="store_true")
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--hype", action="store_true")
    parser.add_argument("--no-sound", action="store_true")
    args = parser.parse_args()

    repo = os.path.abspath(args.repo)
    color_enabled = (not args.no_color) and sys.stdout.isatty()
    sound_enabled = (not args.no_sound) and sys.stdout.isatty()

    running = True

    def _stop(*_):
        nonlocal running
        running = False

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    prev: Optional[Snapshot] = None
    events: Deque[str] = collections.deque(maxlen=100)
    frame = 0
    ui = UIState()

    with InputReader() as inp:
        while running:
            snap = load_snapshot(repo)
            added = detect_events(prev, snap, events)
            if added > 0 and sound_enabled:
                os.write(sys.stdout.fileno(), b"\a")

            render(repo, snap, events, frame, color_enabled, args.hype, ui)
            prev = snap
            frame += 1

            if args.once:
                break

            key = inp.read_key()
            if key in ("q", "Q"):
                break
            elif key == "1":
                ui.view = VIEW_OVERVIEW
                ui.scroll = 0
            elif key == "2":
                ui.view = VIEW_SECTIONS
                ui.scroll = 0
            elif key == "3":
                ui.view = VIEW_EXPERIENCE
                ui.scroll = 0
            elif key == "4":
                ui.view = VIEW_ISSUES
                ui.scroll = 0
            elif key in (KEY_UP,):
                ui.scroll = max(0, ui.scroll - 1)
            elif key in (KEY_DOWN,):
                ui.scroll += 1

            time.sleep(max(0.1, args.interval))

    os.write(sys.stdout.fileno(), b"\033[?25h" + RESET.encode() + b"\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
