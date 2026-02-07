import { useEffect, useRef } from "react";
import { useWS } from "../contexts/WebSocketContext";

const ORG_STORAGE_KEY = "otter-camp-org-id";
const RESUBSCRIBE_INTERVAL_MS = 1500;

export default function WebSocketOrgSubscriber() {
  const { connected, sendMessage } = useWS();
  const lastSentOrgIdRef = useRef<string | null>(null);

  useEffect(() => {
    if (!connected) {
      lastSentOrgIdRef.current = null;
      return;
    }

    const subscribeToCurrentOrg = () => {
      let orgId = "";
      try {
        orgId = (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
      } catch {
        orgId = "";
      }

      if (!orgId) {
        return;
      }
      if (lastSentOrgIdRef.current === orgId) {
        return;
      }

      sendMessage({ type: "subscribe", org_id: orgId });
      lastSentOrgIdRef.current = orgId;
    };

    const handleVisibilityChange = () => {
      if (!document.hidden) {
        subscribeToCurrentOrg();
      }
    };

    subscribeToCurrentOrg();

    const intervalId = window.setInterval(
      subscribeToCurrentOrg,
      RESUBSCRIBE_INTERVAL_MS,
    );
    window.addEventListener("storage", subscribeToCurrentOrg);
    window.addEventListener("focus", subscribeToCurrentOrg);
    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      window.clearInterval(intervalId);
      window.removeEventListener("storage", subscribeToCurrentOrg);
      window.removeEventListener("focus", subscribeToCurrentOrg);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [connected, sendMessage]);

  return null;
}
