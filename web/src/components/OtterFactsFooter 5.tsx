import { useCallback, useMemo, useState } from "react";
import { otterFacts } from "../data/otterFacts";

const totalFacts = otterFacts.length;

const getRandomIndex = (exclude?: number) => {
  if (totalFacts <= 1) {
    return 0;
  }

  let nextIndex = Math.floor(Math.random() * totalFacts);
  while (nextIndex === exclude) {
    nextIndex = Math.floor(Math.random() * totalFacts);
  }
  return nextIndex;
};

export default function OtterFactsFooter() {
  const [factIndex, setFactIndex] = useState(() => getRandomIndex());

  const fact = useMemo(() => otterFacts[factIndex], [factIndex]);

  const handleRefresh = useCallback(() => {
    setFactIndex((current) => getRandomIndex(current));
  }, []);

  return (
    <footer className="border-t border-slate-200 bg-white/90 px-6 py-10 text-slate-700 shadow-inner backdrop-blur dark:border-slate-800 dark:bg-slate-950/80 dark:text-slate-200">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
        <div className="flex flex-wrap items-center gap-3 text-sm font-semibold uppercase tracking-[0.3em] text-emerald-600 dark:text-emerald-300">
          <span className="text-2xl" aria-hidden="true">
            🦦
          </span>
          Otter facts
        </div>
        <p className="text-lg font-medium text-slate-800 dark:text-slate-100">
          {fact}
        </p>
        <div className="flex flex-wrap items-center gap-4">
          <button
            type="button"
            onClick={handleRefresh}
            className="rounded-full bg-emerald-600 px-5 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-emerald-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-emerald-400"
          >
            New fact
          </button>
          <span className="text-sm text-slate-500 dark:text-slate-400">
            Tap for another splash of otter trivia.
          </span>
        </div>
      </div>
    </footer>
  );
}
