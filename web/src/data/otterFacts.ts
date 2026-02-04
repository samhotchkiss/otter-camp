import rawFacts from "./otter-facts.json";

type RawFact = {
  id: number;
  fact: string;
  category: string;
};

const parsedFacts = rawFacts as RawFact[];

export const otterFacts = parsedFacts.map((entry) => entry.fact) as const;

export type OtterFact = (typeof otterFacts)[number];
