import { useEffect, useMemo, useState } from "react";

type TimestampValue = string | number | Date | null | undefined;

export type LiveTimestampProps = {
  timestamp?: TimestampValue;
  className?: string;
};

const parseTimestamp = (value: TimestampValue): Date | null => {
  if (value === null || value === undefined) {
    return null;
  }
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return null;
  }
  return date;
};

export function formatLiveTimestamp(
  value: TimestampValue,
  now: Date = new Date(),
): string {
  const parsed = parseTimestamp(value);
  if (!parsed) {
    return "Never";
  }

  const deltaSeconds = Math.floor((now.getTime() - parsed.getTime()) / 1000);
  if (deltaSeconds < 0) {
    const future = Math.abs(deltaSeconds);
    if (future < 60) {
      return `in ${future}s`;
    }
    if (future < 3600) {
      return `in ${Math.floor(future / 60)}m`;
    }
    if (future < 86400) {
      return `in ${Math.floor(future / 3600)}h`;
    }
    return `in ${Math.floor(future / 86400)}d`;
  }
  if (deltaSeconds <= 0) {
    return "Just now";
  }
  if (deltaSeconds < 60) {
    return `${deltaSeconds}s ago`;
  }
  if (deltaSeconds < 3600) {
    return `${Math.floor(deltaSeconds / 60)}m ago`;
  }
  if (deltaSeconds < 86400) {
    return `${Math.floor(deltaSeconds / 3600)}h ago`;
  }
  return `${Math.floor(deltaSeconds / 86400)}d ago`;
}

export default function LiveTimestamp({
  timestamp,
  className,
}: LiveTimestampProps) {
  const [nowMs, setNowMs] = useState(() => Date.now());

  useEffect(() => {
    const intervalID = window.setInterval(() => {
      setNowMs(Date.now());
    }, 1000);
    return () => {
      window.clearInterval(intervalID);
    };
  }, []);

  const text = useMemo(
    () => formatLiveTimestamp(timestamp, new Date(nowMs)),
    [nowMs, timestamp],
  );
  const parsed = parseTimestamp(timestamp);

  return (
    <span className={className} title={parsed ? parsed.toISOString() : undefined}>
      {text}
    </span>
  );
}
