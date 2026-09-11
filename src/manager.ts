import { CliError, errorMessage } from "./errors.ts";
import { printHealthReport, waitForHealth, type WaitOptions } from "./health.ts";
import type { CommandRunner } from "./process.ts";
import type { Confirm } from "./prompts.ts";
import { DockerRuntime } from "./runtimes/docker.ts";
import { MicrosandboxRuntime } from "./runtimes/microsandbox.ts";
import { TartRuntime } from "./runtimes/tart.ts";
import type {
  ExecuteAction,
  Config,
  RuntimeAdapter,
  RuntimeName,
} from "./types.ts";

export type AdapterMap = Record<RuntimeName, RuntimeAdapter>;

export type WaitForHealthFn = (
  endpoint: string,
  config: Config,
  options?: WaitOptions,
) => Promise<void>;

export interface RuntimeManagerOptions {
  adapters?: AdapterMap;
  isInteractive?: () => boolean;
  waitForHealth?: WaitForHealthFn;
}

export class RuntimeManager {
  private readonly adapters: AdapterMap;
  private readonly isInteractive: () => boolean;
  private readonly awaitHealth: WaitForHealthFn;

  constructor(
    private readonly runner: CommandRunner,
    private readonly ask: Confirm,
    options: RuntimeManagerOptions = {},
  ) {
    this.adapters = options.adapters ?? {
      docker: new DockerRuntime(runner),
      microsandbox: new MicrosandboxRuntime(runner),
      tart: new TartRuntime(runner),
    };
    this.isInteractive = options.isInteractive ?? (() => Boolean(process.stdin.isTTY));
    this.awaitHealth = options.waitForHealth ?? waitForHealth;
  }

  async execute(action: ExecuteAction, runtime: RuntimeName | undefined, config: Config): Promise<number> {
    if (action === "stop") {
      await this.stopAll(config);
      return 0;
    }
    if (action === "check") {
      await this.check(config);
      return 0;
    }
    if (action === "help") return 0;

    const selected = runtime ?? config.runtime;
    if (!selected) {
      throw new CliError(
        "Select --docker, --microsandbox, or --tart, or set RUNTIME in .env.",
        2,
      );
    }
    const selectedConfig: Config = { ...config, runtime: selected };
    const adapter = this.adapters[selected];

    if (action === "start" || action === "restart" || action === "code") {
      await this.prepare(selected, selectedConfig);
    }
    if (action === "code") return this.code(adapter, selectedConfig);

    await adapter[action](selectedConfig);
    return 0;
  }

  async runningRuntimes(config: Config): Promise<RuntimeName[]> {
    const entries = Object.entries(this.adapters) as [RuntimeName, RuntimeAdapter][];
    const states = await Promise.all(entries.map(async ([name, adapter]) => [name, await adapter.isRunning(config)] as const));
    return states.filter(([, running]) => running).map(([name]) => name);
  }

  private async prepare(requested: RuntimeName, config: Config): Promise<void> {
    const conflicts = (await this.runningRuntimes(config)).filter((active) => active !== requested);
    if (conflicts.length === 0) return;
    const activeNames = conflicts.join(", ");
    if (!this.isInteractive()) {
      throw new CliError(`${activeNames} is already running. Run 'just-code stop' before starting ${requested}.`);
    }
    if (!(await this.ask(`${activeNames} is already running. Stop it and start ${requested}?`))) {
      throw new CliError(`Keeping ${activeNames} running.`);
    }
    for (const active of conflicts) await this.adapters[active].stop(config);
  }

  private async code(adapter: RuntimeAdapter, config: Config): Promise<number> {
    await adapter.start(config);
    const endpoint = await adapter.getEndpoint(config);

    let progressReported = false;
    console.log("Waiting for the backend to become healthy...");
    try {
      await this.awaitHealth(endpoint, config, {
        onProgress: (elapsedMs, timeoutMs) => {
          progressReported = true;
          console.log(
            `still waiting (${Math.round(elapsedMs / 1_000)}s/${Math.round(timeoutMs / 1_000)}s)`,
          );
        },
      });
    } catch (error) {
      console.error(errorMessage(error));
      console.error(
        `${adapter.name} is still running. The backend may still be starting up ` +
          `(a first start installs the runtime toolchain and can take several minutes). ` +
          `Logs: 'just-code logs --${adapter.name}'. Stop: 'just-code stop'. ` +
          `Raise the wait with JUST_CODE_START_TIMEOUT=600.`,
      );
      return 1;
    }
    if (progressReported) console.log("Backend is healthy.");

    try {
      return await this.runner.foreground("opencode", [
        "attach",
        endpoint,
        "--username",
        config.opencodeUsername,
        "--password",
        config.opencodePassword,
      ]);
    } finally {
      if (await adapter.isRunning(config)) {
        const shouldStop = await this.ask(`Stop the ${adapter.name} runtime?`);
        if (shouldStop) await adapter.stop(config);
        else console.log(`${adapter.name} left running. Run 'just-code stop' when finished.`);
      }
    }
  }

  private async stopAll(config: Config): Promise<void> {
    const running = await this.runningRuntimes(config);
    if (running.length === 0) {
      console.log("No just-code runtime is running.");
      return;
    }
    const failures: string[] = [];
    for (const runtime of running) {
      try {
        await this.adapters[runtime].stop(config);
      } catch (error) {
        failures.push(`${runtime}: ${error instanceof Error ? error.message : String(error)}`);
      }
    }
    if (failures.length > 0) throw new CliError(`Failed to stop runtime(s): ${failures.join("; ")}`);
  }

  private async check(config: Config): Promise<void> {
    const running = await this.runningRuntimes(config);
    if (running.length === 0) throw new CliError("No just-code runtime is running.");
    if (running.length > 1) {
      throw new CliError("Multiple just-code runtimes are running; run 'just-code stop' first.");
    }

    const runtime = running[0];
    if (!runtime) throw new CliError("No just-code runtime is running.");
    let selectedConfig: Config = { ...config, runtime };
    if (runtime === "tart") {
      const tart = this.adapters.tart;
      if (!(tart instanceof TartRuntime)) throw new CliError("Tart adapter does not expose VM discovery.");
      const vms = await tart.runningVmNames();
      if (vms.length !== 1) {
        throw new CliError(
          vms.length === 0
            ? "No just-code Tart VM is running."
            : "Multiple Tart VMs are running; run 'just-code stop' first.",
        );
      }
      const tartVm = vms[0];
      if (!tartVm) throw new CliError("No just-code Tart VM is running.");
      selectedConfig = { ...selectedConfig, tartVm };
    }
    const endpoint = await this.adapters[runtime].getEndpoint(selectedConfig);
    await printHealthReport(endpoint, selectedConfig);
  }
}
