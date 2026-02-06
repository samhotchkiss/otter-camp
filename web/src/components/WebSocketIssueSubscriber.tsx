import { useEffect, useRef } from "react";
import { useWS } from "../contexts/WebSocketContext";

const ORG_STORAGE_KEY = "otter-camp-org-id";

type WebSocketIssueSubscriberProps = {
  issueID?: string | null;
};

function storedOrgID(): string {
  try {
    return (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function issueChannel(issueID: string): string {
  return `issue:${issueID.trim()}`;
}

export default function WebSocketIssueSubscriber({
  issueID,
}: WebSocketIssueSubscriberProps) {
  const { connected, sendMessage } = useWS();
  const activeChannelRef = useRef<string | null>(null);

  useEffect(() => {
    const orgID = storedOrgID();
    const normalizedIssueID = (issueID ?? "").trim();
    const nextChannel =
      connected && orgID && normalizedIssueID
        ? issueChannel(normalizedIssueID)
        : null;
    const previousChannel = activeChannelRef.current;

    if (previousChannel && previousChannel !== nextChannel) {
      sendMessage({
        type: "unsubscribe",
        org_id: orgID,
        channel: previousChannel,
      });
      activeChannelRef.current = null;
    }

    if (nextChannel && previousChannel !== nextChannel) {
      sendMessage({
        type: "subscribe",
        org_id: orgID,
        channel: nextChannel,
      });
      activeChannelRef.current = nextChannel;
    }

    if (!nextChannel) {
      activeChannelRef.current = null;
    }
  }, [connected, issueID, sendMessage]);

  useEffect(() => {
    return () => {
      const activeChannel = activeChannelRef.current;
      if (!activeChannel) {
        return;
      }
      const orgID = storedOrgID();
      if (!orgID) {
        return;
      }
      sendMessage({
        type: "unsubscribe",
        org_id: orgID,
        channel: activeChannel,
      });
      activeChannelRef.current = null;
    };
  }, [sendMessage]);

  return null;
}
