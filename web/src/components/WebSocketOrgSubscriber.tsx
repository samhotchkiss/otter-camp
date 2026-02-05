import { useEffect, useRef } from "react";
import { useWS } from "../contexts/WebSocketContext";

const ORG_STORAGE_KEY = "otter-camp-org-id";

export default function WebSocketOrgSubscriber() {
  const { connected, sendMessage } = useWS();
  const lastSentOrgIdRef = useRef<string | null>(null);

  useEffect(() => {
    if (!connected) {
      lastSentOrgIdRef.current = null;
      return;
    }

    let orgId = "";
    try {
      orgId = (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
    } catch {
      orgId = "";
    }

    if (!orgId) return;
    if (lastSentOrgIdRef.current === orgId) return;

    sendMessage({ type: "subscribe", org_id: orgId });
    lastSentOrgIdRef.current = orgId;
  }, [connected, sendMessage]);

  return null;
}

