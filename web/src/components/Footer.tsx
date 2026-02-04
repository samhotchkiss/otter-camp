import { useMemo } from "react";

const otterFacts = [
  "Otters hold hands while sleeping so they do not drift apart.",
  "Sea otters have the densest fur of any mammal.",
  "Otters have a favorite rock they keep in a pouch.",
  "A group of otters is called a raft in water and a romp on land.",
  "Otters can close their ears and nostrils underwater.",
  "Sea otters eat about 25% of their body weight daily.",
  "Baby otters are called pups and cannot swim at birth.",
  "Otters have been seen juggling rocks for fun.",
];

export default function Footer() {
  const fact = useMemo(() => {
    return otterFacts[Math.floor(Math.random() * otterFacts.length)];
  }, []);

  return (
    <footer className="border-t border-otter-dark-border bg-otter-dark-surface px-6 py-6 text-center text-sm text-otter-dark-text-muted">
      <div className="mx-auto flex max-w-4xl flex-col items-center gap-2">
        <p>🦦 {fact}</p>
        <p>Made with 🤍 in Santa Fe</p>
        <a
          className="text-otter-dark-text-muted transition hover:text-otter-dark-accent"
          href="https://www.seaotters.org"
          rel="noreferrer"
          target="_blank"
        >
          Help real otters →
        </a>
      </div>
    </footer>
  );
}
