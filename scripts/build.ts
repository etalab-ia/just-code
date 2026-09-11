import { mkdir, rm } from "node:fs/promises";
import { join } from "node:path";

import { CliError } from "../src/errors.ts";

const TARGETS = [
  { artifact: "darwin-arm64", bun: "bun-darwin-arm64" },
  { artifact: "darwin-x64", bun: "bun-darwin-x64" },
  { artifact: "linux-arm64", bun: "bun-linux-arm64" },
  { artifact: "linux-x64", bun: "bun-linux-x64-baseline" },
] as const;

const root = join(import.meta.dir, "..");
const dist = join(root, "dist");

await rm(dist, { recursive: true, force: true });
await mkdir(dist, { recursive: true });

for (const target of TARGETS) {
  const output = join(dist, `just-code-${target.artifact}`);
  console.log(`Building ${target.artifact}...`);
  const subprocess = Bun.spawn([
    "bun",
    "build",
    "--compile",
    "--compile-autoload-dotenv",
    `--target=${target.bun}`,
    `--outfile=${output}`,
    join(root, "src", "cli.ts"),
  ], { cwd: root, stdout: "inherit", stderr: "inherit" });
  const exitCode = await subprocess.exited;
  if (exitCode !== 0) throw new CliError(`Build failed for ${target.artifact}.`, exitCode);
}

console.log(`Built ${TARGETS.length} standalone binaries in dist/.`);
