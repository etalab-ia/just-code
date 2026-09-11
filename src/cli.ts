#!/usr/bin/env bun

import { parseArguments } from "./arguments.ts";
import { loadConfig } from "./config.ts";
import { CliError, errorMessage } from "./errors.ts";
import { RuntimeManager } from "./manager.ts";
import { SystemCommandRunner } from "./process.ts";
import { confirm } from "./prompts.ts";
import packageJson from "../package.json" with { type: "json" };

export const VERSION = packageJson.version;

const HELP = `just-code ${VERSION}

Usage:
  just-code [command] [--docker | --microsandbox | --tart]

Run just-code with no command to start the selected backend and attach the
native OpenCode TUI.

Commands:
  start      Start a backend without attaching the TUI
  stop       Stop every running just-code runtime
  check      Check the active backend and Albert provider
  build      Build or pull the selected runtime image
  restart    Recreate the selected sandbox (destructive)
  logs       Follow logs for the selected runtime
  shell      Open a shell inside the selected runtime
  clean      Remove the selected sandbox and its local state
  doctor     Check the selected runtime installation
  help       Show this help

Runtime selection:
  Pass --docker, --microsandbox, or --tart. RUNTIME in .env is used when no
  flag is provided; an explicit flag always takes precedence.
`;

export async function run(argv = process.argv.slice(2)): Promise<number> {
  try {
    const parsed = parseArguments(argv);
    if (parsed.version) {
      console.log(VERSION);
      return 0;
    }
    if (parsed.action === "help") {
      process.stdout.write(HELP);
      return 0;
    }

    const ignoresRuntimePreference = parsed.action === "stop" || parsed.action === "check";
    const config = loadConfig(process.env, process.cwd(), !parsed.runtime && !ignoresRuntimePreference);
    const manager = new RuntimeManager(new SystemCommandRunner(), confirm);
    return await manager.execute(parsed.action, parsed.runtime, config);
  } catch (error) {
    console.error(errorMessage(error));
    return error instanceof CliError ? error.exitCode : 1;
  }
}

if (import.meta.main) process.exitCode = await run();
