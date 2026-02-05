import { useCallback, useEffect, useMemo, useState } from "react";
import { otterFacts } from "../data/otterFacts";

type FooterProps = {
  refreshIntervalMs?: number;
};

const DEFAULT_REFRESH_INTERVAL_MS = 30_000;
const totalFacts = otterFacts.length;

function getRandomIndex(exclude?: number) {
  if (totalFacts <= 1) {
    return 0;
  }

  let nextIndex = Math.floor(Math.random() * totalFacts);
  while (nextIndex === exclude) {
    nextIndex = Math.floor(Math.random() * totalFacts);
  }
  return nextIndex;
}

export default function Footer({
  refreshIntervalMs = DEFAULT_REFRESH_INTERVAL_MS,
}: FooterProps) {
  const [factIndex, setFactIndex] = useState(() => getRandomIndex());

  const fact = otterFacts[factIndex];

  const refreshFact = useCallback(() => {
    setFactIndex((current) => getRandomIndex(current));
  }, []);

  useEffect(() => {
    if (refreshIntervalMs <= 0 || totalFacts <= 1) {
      return;
    }

    const intervalId = window.setInterval(refreshFact, refreshIntervalMs);
    return () => window.clearInterval(intervalId);
  }, [refreshFact, refreshIntervalMs]);

  const buildInfo = useMemo(() => {
    const buildDate = new Date(__BUILD_TIME__);
    const buildLabel = Number.isNaN(buildDate.getTime())
      ? __BUILD_TIME__
      : new Intl.DateTimeFormat(undefined, {
          year: "numeric",
          month: "short",
          day: "2-digit",
          hour: "2-digit",
          minute: "2-digit",
        }).format(buildDate);

    const shaShort =
      __BUILD_SHA__ === "dev" ? "dev" : __BUILD_SHA__.slice(0, 8);

    return {
      version: __APP_VERSION__,
      shaShort,
      shaFull: __BUILD_SHA__,
      buildTime: __BUILD_TIME__,
      buildLabel,
    };
  }, []);

  return (
    <footer className="shrink-0 border-t border-otter-border bg-otter-surface/85 px-4 py-5 backdrop-blur-sm dark:border-otter-dark-border dark:bg-otter-dark-surface/85 md:px-6">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-4 md:flex-row md:items-center md:justify-between">
        <div className="flex flex-col gap-2 md:max-w-4xl">
          <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.22em] text-otter-muted dark:text-otter-dark-muted">
            <span aria-hidden="true">🦦</span>
            <span>Otter fact</span>
          </div>

          <button
            type="button"
            onClick={refreshFact}
            className="-mx-2 rounded-xl px-2 py-1 text-left text-sm font-medium leading-relaxed text-otter-text transition hover:bg-otter-surface-alt focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-otter-accent dark:text-otter-dark-text dark:hover:bg-otter-dark-surface-alt dark:focus-visible:outline-otter-dark-accent md:text-base"
            aria-label="Show a new otter fact"
            title="Click for a new otter fact"
          >
            {fact}
          </button>

          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-otter-muted dark:text-otter-dark-muted">
            <span>Click for another.</span>
            <span aria-hidden="true">•</span>
            <span>
              Auto-refreshes every{" "}
              {Math.round(refreshIntervalMs / 1000).toString()}s.
            </span>
          </div>
        </div>

        <div className="flex flex-col items-start gap-1 text-xs text-otter-muted dark:text-otter-dark-muted md:items-end">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="font-semibold text-otter-text dark:text-otter-dark-text">
              v{buildInfo.version}
            </span>
            <span aria-hidden="true">•</span>
            <span title={buildInfo.shaFull}>build {buildInfo.shaShort}</span>
          </div>
          <time dateTime={buildInfo.buildTime} title={buildInfo.buildTime}>
            Built {buildInfo.buildLabel}
          </time>
        </div>
      </div>
    </footer>
  );
}
