# Issue #5: iOS Background/Foreground Triggers Disconnected + Reconnected Toast Spam

## Problem

On iOS Safari, when the user leaves the browser (switches to another app, locks the phone, etc.) and comes back, they get two toasts in rapid succession:

1. ❌ "Connection lost — Attempting to reconnect..."
2. ✅ "Reconnected — Connection to server restored"

This is jarring and unnecessary — the user didn't do anything wrong, and the reconnection is automatic and near-instant. On mobile, background tab suspension is normal behavior, not an error condition.

## Root Cause

**File:** `web/src/components/WebSocketToastHandler.tsx`

The handler fires toasts on every `connected` state transition:

```tsx
// Connection lost
if (!connected && wasConnected.current) {
  error("Connection lost", "Attempting to reconnect...");
}

// Connection restored
if (connected && !wasConnected.current) {
  success("Reconnected", "Connection to server restored");
}
```

**File:** `web/src/hooks/useWebSocket.ts`

The WebSocket hook doesn't account for page visibility. When iOS suspends the tab, the WS connection drops. When the tab resumes, it reconnects. Both state changes fire in quick succession, producing the toast double-tap.

## Fix

### Option A: Suppress toasts for fast reconnects (simplest)

In `WebSocketToastHandler.tsx`, add a debounce: if the connection drops and restores within ~3 seconds, show nothing (or just the "Reconnected" toast, not the error).

```tsx
const disconnectTimeRef = useRef<number | null>(null);

// Connection lost — record time but delay the toast
if (!connected && wasConnected.current) {
  disconnectTimeRef.current = Date.now();
  // Don't show error toast immediately — wait to see if we reconnect fast
}

// Connection restored
if (connected && !wasConnected.current) {
  const downMs = disconnectTimeRef.current 
    ? Date.now() - disconnectTimeRef.current 
    : Infinity;
  disconnectTimeRef.current = null;
  
  if (downMs > 3000) {
    // Was down for a while — show reconnected toast
    success("Reconnected", "Connection to server restored");
  }
  // If < 3s, silently reconnected — no toast needed
}
```

Also clear any pending "disconnected" toast timeout on reconnect.

### Option B: Use Page Visibility API (more robust)

In the WebSocket hook (`useWebSocket.ts`), listen for `visibilitychange`:

```tsx
useEffect(() => {
  const handleVisibility = () => {
    if (document.visibilityState === 'visible') {
      // Tab became visible — reconnect immediately if disconnected
      // Mark this as a "background resume" so toast handler can suppress
    }
  };
  document.addEventListener('visibilitychange', handleVisibility);
  return () => document.removeEventListener('visibilitychange', handleVisibility);
}, []);
```

Pass a `reconnectReason` (e.g., `"visibility"` vs `"error"`) through the WebSocket context so the toast handler can distinguish between "iOS woke up" and "server actually went down."

### Recommended

Combine both: Option A for the toast layer (debounce fast reconnects regardless of cause) + Option B for the WS layer (proactive reconnect on visibility change, skip backoff delay).

## Testing

- [ ] On iOS Safari: leave app → return → no toast spam (or at most a brief "Reconnected" if down > 3s)
- [ ] On desktop: simulate network disconnect for 5+ seconds → see both "disconnected" and "reconnected" toasts (real outage, user should know)
- [ ] Fast network blip (< 3s) → no toasts on any platform
- [ ] Long outage (> 30s) → "Connection lost" toast appears after 3s delay, "Reconnected" when restored

## Files to Modify

- `web/src/components/WebSocketToastHandler.tsx` — debounce logic
- `web/src/hooks/useWebSocket.ts` — visibility-aware reconnect
- `web/src/contexts/WebSocketContext.tsx` — optionally expose reconnect reason
