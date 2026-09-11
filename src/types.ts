export const RUNTIME_NAMES = ["docker", "microsandbox", "tart"] as const;
export type RuntimeName = (typeof RUNTIME_NAMES)[number];

export const ACTION_NAMES = [
  "start",
  "stop",
  "build",
  "restart",
  "logs",
  "check",
  "shell",
  "clean",
  "doctor",
  "help",
] as const;
export type ActionName = (typeof ACTION_NAMES)[number];

/**
 * Running `just-code` with no command starts the selected backend and attaches
 * the native OpenCode TUI. It is deliberately absent from ACTION_NAMES: there is
 * no `code` command to type.
 */
export const DEFAULT_ACTION = "code" as const;
export type ExecuteAction = ActionName | typeof DEFAULT_ACTION;

export interface Config {
  runtime?: RuntimeName;
  projectDir: string;
  stateDir: string;
  albertApiKey?: string;
  opencodePassword: string;
  opencodeUsername: string;
  port: number;
  msbImage: string;
  msbSandbox: string;
  tartImage: string;
  tartVm: string;
  tartMtu: string;
  /**
   * How long to wait for the backend health endpoint after starting a runtime.
   * First starts are slow: Microsandbox installs its toolchain inside the VM,
   * Docker builds the image, and Tart installs OpenCode in the guest.
   */
  startTimeoutMs: number;
}

export interface RuntimeAdapter {
  readonly name: RuntimeName;
  isRunning(config: Config): Promise<boolean>;
  start(config: Config): Promise<void>;
  stop(config: Config): Promise<void>;
  build(config: Config): Promise<void>;
  restart(config: Config): Promise<void>;
  logs(config: Config): Promise<void>;
  shell(config: Config): Promise<void>;
  clean(config: Config): Promise<void>;
  doctor(config: Config): Promise<void>;
  getEndpoint(config: Config): Promise<string>;
}
