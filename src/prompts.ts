import { createInterface } from "node:readline/promises";

export type Confirm = (question: string) => Promise<boolean>;

export const confirm: Confirm = async (question) => {
  if (!process.stdin.isTTY) return false;
  const readline = createInterface({ input: process.stdin, output: process.stdout });
  try {
    const answer = (await readline.question(`${question} [y/N] `)).trim().toLowerCase();
    return answer === "y" || answer === "yes";
  } finally {
    readline.close();
  }
};
