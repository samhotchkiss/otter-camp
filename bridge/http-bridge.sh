#!/bin/bash
# Simple HTTP-based bridge for OpenClaw → Otter Camp
# This script uses curl to fetch sessions and push to Otter Camp
# 
# Usage:
#   OPENCLAW_TOKEN=xxx ./bridge/http-bridge.sh
#
# Or add to crontab:
#   * * * * * /path/to/http-bridge.sh

OPENCLAW_HOST="${OPENCLAW_HOST:-127.0.0.1}"
OPENCLAW_PORT="${OPENCLAW_PORT:-18791}"
OPENCLAW_TOKEN="${OPENCLAW_TOKEN:-75de3e0770ce81208b5c1f24b3dc1667e21379348f6d56f6}"
OTTERCAMP_URL="${OTTERCAMP_URL:-https://api.otter.camp}"

echo "$(date): Fetching sessions from OpenClaw..."

# OpenClaw doesn't have a direct HTTP API for sessions, so we need to use the bridge script
# For now, this is a placeholder - the TypeScript bridge is the real implementation

echo "Use the TypeScript bridge instead: npx tsx bridge/openclaw-bridge.ts"
