import { useMemo } from "react";
import useNowTicker from "../hooks/useNowTicker";

type TimestampValue = string | number | Date | null | undefined;

export type LiveTimestampProps = {
  timestamp?: TimestampValue;
  className?: string;
  verbose?: boolean;
};

type LiveTimestampFormatOptions = {
  verbose?: boolean;
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
  options: LiveTimestampFormatOptions = {},
): string {
  const parsed = parseTimestamp(value);
  if (!parsed) {
    return "Never";
  }

  const verbose = options.verbose === true;

  const deltaSeconds = Math.floor((now.getTime() - parsed.getTime()) / 1000);
  if (deltaSeconds < 0) {
    const future = Math.abs(deltaSeconds);
    if (verbose) {
      if (future < 60) {
        const unit = future === 1 ? "second" : "seconds";
        return `in ${future} ${unit}`;
      }
      if (future < 3600) {
        const minutes = Math.floor(future / 60);
        const unit = minutes === 1 ? "minute" : "minutes";
        return `in ${minutes} ${unit}`;
      }
      if (future < 86400) {
        const hours = Math.floor(future / 3600);
        const unit = hours === 1 ? "hour" : "hours";
        return `in ${hours} ${unit}`;
      }
      const days = Math.floor(future / 86400);
      const unit = days === 1 ? "day" : "days";
      return `in ${days} ${unit}`;
    }
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
    return verbose ? "just now" : "Just now";
  }
  if (verbose) {
    if (deltaSeconds <= 5) {
      return "just now";
    }
    if (deltaSeconds < 60) {
      const unit = deltaSeconds === 1 ? "second" : "seconds";
      return `${deltaSeconds} ${unit} ago`;
    }
    if (deltaSeconds < 3600) {
      const minutes = Math.floor(deltaSeconds / 60);
      const unit = minutes === 1 ? "minute" : "minutes";
      return `${minutes} ${unit} ago`;
    }
    if (deltaSeconds < 86400) {
      const hours = Math.floor(deltaSeconds / 3600);
      const unit = hours === 1 ? "hour" : "hours";
      return `${hours} ${unit} ago`;
    }
    const days = Math.floor(deltaSeconds / 86400);
    const unit = days === 1 ? "day" : "days";
    return `${days} ${unit} ago`;
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
  verbose = false,
}: LiveTimestampProps) {
  const nowMs = useNowTicker();

  const text = useMemo(
    () => formatLiveTimestamp(timestamp, new Date(nowMs), { verbose }),
    [nowMs, timestamp, verbose],
  );
  const parsed = parseTimestamp(timestamp);

  return (
    <span className={className} title={parsed ? parsed.toISOString() : undefined}>
      {text}
    </span>
  );
}
