import { mkdir } from "node:fs/promises";
import { join } from "node:path";

import { materializeAssets } from "../assets.ts";
import { requireAlbertApiKey, validateTartMtu } from "../config.ts";
import { CliError } from "../errors.ts";
import { isHealthy } from "../health.ts";
import type { CommandRunner } from "../process.ts";
import type { Config, RuntimeAdapter } from "../types.ts";
import { foregroundChecked, printCommandOutput, sleep } from "./shared.ts";

interface TartVm {
  name: string;
  state: string;
}

export function parseTartList(output: string): TartVm[] {
  const vms: TartVm[] = [];
  for (const line of output.split("\n")) {
    const columns = line.trim().split(/\s+/);
    if (columns.length < 3 || columns[0] !== "local") continue;
    const name = columns[1];
    const state = columns.at(-1);
    if (name && state) vms.push({ name, state });
  }
  return vms;
}

export class TartRuntime implements RuntimeAdapter {
  readonly name = "tart" as const;

  constructor(private readonly runner: CommandRunner) {}

  async isRunning(): Promise<boolean> {
    return (await this.runningVmNames()).length > 0;
  }

  async start(config: Config): Promise<void> {
    requireAlbertApiKey(config);
    validateTartMtu(config.tartMtu);
    await Promise.all([
      mkdir(config.projectDir, { recursive: true }),
      mkdir(config.stateDir, { recursive: true }),
    ]);
    const assets = await materializeAssets(config);
    const vms = await this.listVms();
    const target = vms.find((vm) => vm.name === config.tartVm);

    if (target?.state === "running") {
      await this.waitForAgent(config);
      const ip = await this.tryGetIp(config);
      if (ip && await isHealthy(`http://${ip}:${config.port}`, config)) {
        console.log(`${config.tartVm} is running with a healthy OpenCode backend.`);
        return;
      }
      console.log(`${config.tartVm} is running but OpenCode is not healthy; restarting backend...`);
      await this.stopBackend(config);
      await this.launchBackend(config);
      return;
    }

    if (!target) {
      console.log(`Cloning ${config.tartImage} to ${config.tartVm}...`);
      await foregroundChecked(this.runner, "tart", ["clone", config.tartImage, config.tartVm]);
    }

    console.log(`Starting ${config.tartVm} with Tart...`);
    await this.runner.detached("tart", [
      "run",
      "--no-graphics",
      `--dir=workspace:${config.projectDir}`,
      `--dir=just-code:${assets.tartDirectory}:ro`,
      config.tartVm,
    ], this.logFile(config));
    await this.waitForAgent(config);
    await this.launchBackend(config);
  }

  async stop(config: Config): Promise<void> {
    const running = await this.runningVmNames();
    if (running.length === 0) {
      console.log("No just-code Tart VM is running.");
      return;
    }
    for (const vm of running) {
      console.log(`Stopping ${vm}...`);
      const result = await this.runner.checked("tart", ["stop", vm, "--timeout", "5"]);
      printCommandOutput(result.stdout);
    }
  }

  async build(config: Config): Promise<void> {
    await foregroundChecked(this.runner, "tart", ["pull", config.tartImage]);
  }

  async restart(config: Config): Promise<void> {
    await this.clean(config);
    await this.start(config);
  }

  async logs(config: Config): Promise<void> {
    await foregroundChecked(this.runner, "tail", ["-f", this.logFile(config)]);
  }

  async shell(config: Config): Promise<void> {
    await foregroundChecked(this.runner, "tart", ["exec", "-it", config.tartVm, "/bin/zsh"]);
  }

  async clean(config: Config): Promise<void> {
    const target = (await this.listVms()).find((vm) => vm.name === config.tartVm);
    if (!target) {
      console.log(`${config.tartVm} does not exist.`);
      return;
    }
    if (target.state === "running") {
      await foregroundChecked(this.runner, "tart", ["stop", config.tartVm, "--timeout", "5"]);
    }
    await foregroundChecked(this.runner, "tart", ["delete", config.tartVm]);
  }

  async doctor(): Promise<void> {
    await foregroundChecked(this.runner, "tart", ["--version"]);
    console.log("Tart runtime is ready.");
  }

  async getEndpoint(config: Config): Promise<string> {
    const result = await this.runner.checked("tart", ["ip", "--wait", "60", config.tartVm]);
    const ip = result.stdout.trim();
    if (!ip) throw new CliError(`Tart did not return an IP address for ${config.tartVm}.`);
    return `http://${ip}:${config.port}`;
  }

  async runningVmNames(): Promise<string[]> {
    return (await this.listVms())
      .filter((vm) => vm.name.startsWith("opencode-") && vm.state === "running")
      .map((vm) => vm.name);
  }

  private async listVms(): Promise<TartVm[]> {
    if (!this.runner.commandExists("tart")) return [];
    const result = await this.runner.run("tart", ["list"]);
    return result.exitCode === 0 ? parseTartList(result.stdout) : [];
  }

  private async waitForAgent(config: Config): Promise<void> {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      const result = await this.runner.run("tart", ["exec", config.tartVm, "true"]);
      if (result.exitCode === 0) return;
      await sleep(1_000);
    }
    throw new CliError(`Timed out waiting for ${config.tartVm} guest agent.`);
  }

  private async tryGetIp(config: Config): Promise<string | undefined> {
    const result = await this.runner.run("tart", ["ip", "--wait", "60", config.tartVm]);
    return result.exitCode === 0 && result.stdout.trim() ? result.stdout.trim() : undefined;
  }

  private async stopBackend(config: Config): Promise<void> {
    await this.runner.run("tart", ["exec", config.tartVm, "pkill", "-x", "opencode"]);
    for (let attempt = 0; attempt < 10; attempt += 1) {
      const result = await this.runner.run("tart", ["exec", config.tartVm, "pgrep", "-x", "opencode"]);
      if (result.exitCode !== 0) return;
      await sleep(1_000);
    }
    console.error("opencode ignored SIGTERM; force-killing...");
    await this.runner.run("tart", ["exec", config.tartVm, "pkill", "-9", "-x", "opencode"]);
    const remaining = await this.runner.run("tart", ["exec", config.tartVm, "pgrep", "-x", "opencode"]);
    if (remaining.exitCode === 0) {
      throw new CliError("Failed to stop the previous opencode process; refusing to relaunch.");
    }
  }

  private async launchBackend(config: Config): Promise<void> {
    const apiKey = requireAlbertApiKey(config);
    console.log(`Launching OpenCode server inside ${config.tartVm}...`);
    await this.runner.detached("tart", [
      "exec",
      "-i",
      config.tartVm,
      "/bin/sh",
      "/Volumes/My Shared Files/just-code/tart-bootstrap.sh",
      String(config.port),
      config.opencodeUsername,
      config.tartMtu,
    ], this.logFile(config), { input: `${config.opencodePassword}\n${apiKey}\n` });
  }

  private logFile(config: Config): string {
    return join(config.stateDir, "tart.log");
  }
}
