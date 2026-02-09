import { useMemo } from "react";
import type { Emission } from "../hooks/useEmissions";
import useNowTicker from "../hooks/useNowTicker";
import LiveTimestamp from "./LiveTimestamp";

type AgentWorkingIndicatorProps = {
  latestEmission?: Emission | null;
  activeWindowSeconds?: number;
  className?: string;
  activeText?: string;
  idleText?: string;
};

export function isActiveEmission(
  latestEmission: Emission | null | undefined,
  activeWindowSeconds: number,
  now: Date = new Date(),
): boolean {
  if (!latestEmission || !latestEmission.timestamp) {
    return false;
  }
  const emittedAt = new Date(latestEmission.timestamp);
  const emittedMs = emittedAt.getTime();
  if (Number.isNaN(emittedMs)) {
    return false;
  }

  const windowSeconds =
    Number.isFinite(activeWindowSeconds) && activeWindowSeconds > 0
      ? Math.trunc(activeWindowSeconds)
      : 30;
  const deltaSeconds = Math.floor((now.getTime() - emittedMs) / 1000);
  if (deltaSeconds < 0) {
    return Math.abs(deltaSeconds) <= windowSeconds;
  }
  return deltaSeconds <= windowSeconds;
}

export default function AgentWorkingIndicator({
  latestEmission,
  activeWindowSeconds = 30,
  className,
  activeText = "Agent is working",
  idleText = "Idle",
}: AgentWorkingIndicatorProps) {
  const nowMs = useNowTicker();

  const active = useMemo(
    () =>
      isActiveEmission(
        latestEmission,
        activeWindowSeconds,
        new Date(nowMs),
      ),
    [activeWindowSeconds, latestEmission, nowMs],
  );

  return (
    <div className={className} aria-live="polite">
      {active ? (
        <span>
          {activeText}
          {latestEmission?.timestamp ? (
            <>
              {" "}
              · <LiveTimestamp timestamp={latestEmission.timestamp} />
            </>
          ) : null}
        </span>
      ) : (
        <span>{idleText}</span>
      )}
    </div>
  );
}
