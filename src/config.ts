import { homedir } from "node:os";
import { basename, isAbsolute, join, resolve } from "node:path";

import { CliError } from "./errors.ts";
import { RUNTIME_NAMES, type Config, type RuntimeName } from "./types.ts";

const DEFAULT_TART_IMAGE = "ghcr.io/cirruslabs/macos-tahoe-base:latest";
export const DEFAULT_START_TIMEOUT_MS = 300_000;

export function isRuntimeName(value: string): value is RuntimeName {
  return RUNTIME_NAMES.some((runtime) => runtime === value);
}

export function tartVmName(image: string): string {
  const imageName = basename(image)
    .replace(/^macos-/, "")
    .replaceAll(":", "-")
    .replaceAll("@sha256", "-sha256")
    .replace(/[^a-zA-Z0-9_.-]/g, "-");
  if (!imageName) throw new CliError("TART_IMAGE must include an image name.", 2);
  return `opencode-${imageName}`;
}

export function validateTartMtu(value: string): string {
  if (value === "auto") return value;
  if (!/^\d+$/.test(value)) {
    throw new CliError("TART_MTU must be auto or an integer from 1280 to 1500.", 2);
  }
  const parsed = Number(value);
  if (String(parsed) !== value || parsed < 1280 || parsed > 1500) {
    throw new CliError("TART_MTU must be auto or an integer from 1280 to 1500.", 2);
  }
  return value;
}

export function parseStartTimeout(value: string): number {
  if (!/^\d+$/.test(value)) {
    throw new CliError("JUST_CODE_START_TIMEOUT must be a whole number of seconds.", 2);
  }
  const seconds = Number(value);
  if (seconds < 1) {
    throw new CliError("JUST_CODE_START_TIMEOUT must be at least 1 second.", 2);
  }
  return seconds * 1_000;
}

function expandPath(value: string, cwd: string, home: string): string {
  const expanded = value === "~" ? home : value.startsWith("~/") ? join(home, value.slice(2)) : value;
  return isAbsolute(expanded) ? expanded : resolve(cwd, expanded);
}

export interface LoadConfigOptions {
  /** Validate RUNTIME. Disable for commands that never select a runtime. */
  validateRuntime?: boolean;
  /**
   * Validate JUST_CODE_START_TIMEOUT. Only the default attach flow reads it, so
   * a typo there must not block `stop`, `clean`, `logs`, `doctor` or `check`.
   */
  validateStartTimeout?: boolean;
}

export function loadConfig(
  env: Record<string, string | undefined> = process.env,
  cwd = process.cwd(),
  options: LoadConfigOptions = {},
): Config {
  const validateRuntime = options.validateRuntime ?? true;
  const validateStartTimeout = options.validateStartTimeout ?? true;
  const home = env.HOME || homedir();
  const runtimeValue = env.RUNTIME?.trim();
  if (runtimeValue && !isRuntimeName(runtimeValue) && validateRuntime) {
    throw new CliError("RUNTIME must be docker, microsandbox, or tart.", 2);
  }

  const tartImage = env.TART_IMAGE || DEFAULT_TART_IMAGE;
  const workspaceDir = env.WORKSPACE_DIR ?? env.PROJECT_DIR;
  if (env.PROJECT_DIR !== undefined && env.WORKSPACE_DIR === undefined) {
    console.error("Warning: PROJECT_DIR is deprecated; use WORKSPACE_DIR instead.");
  }
  const stateRoot = env.XDG_STATE_HOME
    ? expandPath(env.XDG_STATE_HOME, cwd, home)
    : join(home, ".local", "state");
  const config: Config = {
    workspaceDir: expandPath(workspaceDir || "workspace", cwd, home),
    stateDir: join(stateRoot, "just-code"),
    opencodePassword: env.OPENCODE_SERVER_PASSWORD ?? "albert-dev-pass",
    opencodeUsername: env.OPENCODE_SERVER_USERNAME ?? "opencode",
    port: 4096,
    msbImage: "ghcr.io/anomalyco/opencode:latest",
    msbSandbox: "albert-opencode-sandbox",
    tartImage,
    tartVm: tartVmName(tartImage),
    tartMtu: env.TART_MTU ?? "1280",
    startTimeoutMs:
      env.JUST_CODE_START_TIMEOUT && validateStartTimeout
        ? parseStartTimeout(env.JUST_CODE_START_TIMEOUT)
        : DEFAULT_START_TIMEOUT_MS,
  };
  if (runtimeValue && isRuntimeName(runtimeValue)) config.runtime = runtimeValue;
  if (env.ALBERT_API_KEY) config.albertApiKey = env.ALBERT_API_KEY;
  return config;
}

export function requireAlbertApiKey(config: Config): string {
  if (!config.albertApiKey) {
    throw new CliError("Set ALBERT_API_KEY in the environment or .env.", 2);
  }
  return config.albertApiKey;
}
