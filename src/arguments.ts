import { CliError } from "./errors.ts";
import {
  ACTION_NAMES,
  DEFAULT_ACTION,
  type ActionName,
  type ExecuteAction,
  type RuntimeName,
} from "./types.ts";

export interface ParsedArguments {
  action: ExecuteAction;
  runtime?: RuntimeName;
  version: boolean;
}

const RUNTIME_FLAGS: Record<string, RuntimeName> = {
  "--docker": "docker",
  "--microsandbox": "microsandbox",
  "--tart": "tart",
};

function isAction(value: string): value is ActionName {
  return ACTION_NAMES.some((action) => action === value);
}

export function parseArguments(argv: readonly string[]): ParsedArguments {
  let action: ActionName | undefined;
  let runtime: RuntimeName | undefined;
  let version = false;

  for (const argument of argv) {
    const runtimeFlag = RUNTIME_FLAGS[argument];
    if (runtimeFlag) {
      if (runtime && runtime !== runtimeFlag) throw new CliError("Select exactly one runtime.", 2);
      runtime = runtimeFlag;
      continue;
    }
    if (argument === "-h" || argument === "--help") {
      action = "help";
      continue;
    }
    if (argument === "-V" || argument === "--version") {
      version = true;
      continue;
    }
    if (isAction(argument) && action === undefined) {
      action = argument;
      continue;
    }
    if (argument === DEFAULT_ACTION) {
      throw new CliError(
        "The 'code' command was removed: run 'just-code' with no command to start the backend and attach the TUI.",
        2,
      );
    }
    throw new CliError(`Unknown argument: ${argument}`, 2);
  }

  const parsed: ParsedArguments = { action: action ?? DEFAULT_ACTION, version };
  if (runtime) parsed.runtime = runtime;
  return parsed;
}
