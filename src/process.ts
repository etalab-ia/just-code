import { closeSync, openSync } from "node:fs";

import { CliError } from "./errors.ts";

export interface CommandResult {
  exitCode: number;
  stdout: string;
  stderr: string;
}

export interface CommandOptions {
  cwd?: string;
  env?: Record<string, string | undefined>;
  input?: string;
}

export interface CommandRunner {
  run(command: string, args?: readonly string[], options?: CommandOptions): Promise<CommandResult>;
  checked(command: string, args?: readonly string[], options?: CommandOptions): Promise<CommandResult>;
  foreground(command: string, args?: readonly string[], options?: CommandOptions): Promise<number>;
  detached(
    command: string,
    args: readonly string[],
    logFile: string,
    options?: CommandOptions,
  ): Promise<number>;
  commandExists(command: string): boolean;
}

function mergedEnvironment(overrides?: Record<string, string | undefined>): Record<string, string | undefined> {
  return { ...process.env, ...overrides };
}

function writeInput(
  stdin: number | undefined | import("bun").FileSink,
  input: string | undefined,
): void {
  if (input === undefined || stdin === undefined || typeof stdin === "number") return;
  stdin.write(input);
  stdin.end();
}

export class SystemCommandRunner implements CommandRunner {
  async run(
    command: string,
    args: readonly string[] = [],
    options: CommandOptions = {},
  ): Promise<CommandResult> {
    const subprocess = Bun.spawn([command, ...args], {
      ...(options.cwd === undefined ? {} : { cwd: options.cwd }),
      env: mergedEnvironment(options.env),
      stdin: options.input === undefined ? "ignore" : "pipe",
      stdout: "pipe",
      stderr: "pipe",
    });
    writeInput(subprocess.stdin, options.input);

    const [exitCode, stdout, stderr] = await Promise.all([
      subprocess.exited,
      new Response(subprocess.stdout).text(),
      new Response(subprocess.stderr).text(),
    ]);
    return { exitCode, stdout, stderr };
  }

  async checked(
    command: string,
    args: readonly string[] = [],
    options: CommandOptions = {},
  ): Promise<CommandResult> {
    const result = await this.run(command, args, options);
    if (result.exitCode !== 0) {
      const detail = result.stderr.trim() || result.stdout.trim();
      throw new CliError(`${command} failed with exit code ${result.exitCode}${detail ? `: ${detail}` : ""}`);
    }
    return result;
  }

  async foreground(
    command: string,
    args: readonly string[] = [],
    options: CommandOptions = {},
  ): Promise<number> {
    const subprocess = Bun.spawn([command, ...args], {
      ...(options.cwd === undefined ? {} : { cwd: options.cwd }),
      env: mergedEnvironment(options.env),
      stdin: options.input === undefined ? "inherit" : "pipe",
      stdout: "inherit",
      stderr: "inherit",
    });
    writeInput(subprocess.stdin, options.input);

    const signals = ["SIGINT", "SIGTERM", "SIGHUP"] as const;
    const handlers = signals.map((signal) => {
      const handler = (): void => subprocess.kill(signal);
      process.on(signal, handler);
      return [signal, handler] as const;
    });

    try {
      return await subprocess.exited;
    } finally {
      for (const [signal, handler] of handlers) process.off(signal, handler);
    }
  }

  async detached(
    command: string,
    args: readonly string[],
    logFile: string,
    options: CommandOptions = {},
  ): Promise<number> {
    const logFd = openSync(logFile, "a");
    try {
      const subprocess = Bun.spawn([command, ...args], {
        ...(options.cwd === undefined ? {} : { cwd: options.cwd }),
        detached: true,
        env: mergedEnvironment(options.env),
        stdin: options.input === undefined ? "ignore" : "pipe",
        stdout: logFd,
        stderr: logFd,
      });
      writeInput(subprocess.stdin, options.input);
      subprocess.unref();
      return subprocess.pid;
    } finally {
      closeSync(logFd);
    }
  }

  commandExists(command: string): boolean {
    return Bun.which(command) !== null;
  }
}
