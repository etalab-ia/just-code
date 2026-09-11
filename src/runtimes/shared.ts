import { CliError } from "../errors.ts";
import type { CommandOptions, CommandRunner } from "../process.ts";

export async function foregroundChecked(
  runner: CommandRunner,
  command: string,
  args: readonly string[] = [],
  options: CommandOptions = {},
): Promise<void> {
  const exitCode = await runner.foreground(command, args, options);
  if (exitCode !== 0) throw new CliError(`${command} failed with exit code ${exitCode}.`, exitCode);
}

export function printCommandOutput(stdout: string): void {
  if (stdout.length > 0) process.stdout.write(stdout);
}

export function sleep(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
