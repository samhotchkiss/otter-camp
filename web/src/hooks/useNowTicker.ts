import { useEffect, useState } from "react";

const TICK_MS = 1000;

let tickerNowMs = Date.now();
let tickerIntervalID: number | null = null;
const tickerSubscribers = new Set<(nowMs: number) => void>();

const startTicker = () => {
  if (typeof window === "undefined" || tickerIntervalID !== null) {
    return;
  }
  tickerIntervalID = window.setInterval(() => {
    tickerNowMs = Date.now();
    for (const notify of tickerSubscribers) {
      notify(tickerNowMs);
    }
  }, TICK_MS);
};

const stopTickerIfIdle = () => {
  if (typeof window === "undefined") {
    return;
  }
  if (tickerSubscribers.size > 0 || tickerIntervalID === null) {
    return;
  }
  window.clearInterval(tickerIntervalID);
  tickerIntervalID = null;
};

export default function useNowTicker(): number {
  const [nowMs, setNowMs] = useState(() => tickerNowMs);

  useEffect(() => {
    const onTick = (nextNowMs: number) => {
      setNowMs(nextNowMs);
    };

    tickerSubscribers.add(onTick);
    tickerNowMs = Date.now();
    onTick(tickerNowMs);
    startTicker();

    return () => {
      tickerSubscribers.delete(onTick);
      stopTickerIfIdle();
    };
  }, []);

  return nowMs;
}
