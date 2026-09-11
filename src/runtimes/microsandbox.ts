import { mkdir } from "node:fs/promises";

import { materializeAssets } from "../assets.ts";
import { requireAlbertApiKey } from "../config.ts";
import { isHealthy } from "../health.ts";
import type { CommandRunner } from "../process.ts";
import type { Config, RuntimeAdapter } from "../types.ts";
import { foregroundChecked, printCommandOutput } from "./shared.ts";

export interface MsbSandbox {
  name: string;
  status: string;
}

/**
 * Parse `msb ls` output. Columns are space-padded, and only the CREATED column
 * contains a space, so a whitespace split keeps name/image/status in the first
 * three positions.
 */
export function parseMsbLs(output: string): MsbSandbox[] {
  const sandboxes: MsbSandbox[] = [];
  for (const line of output.split("\n")) {
    const columns = line.trim().split(/\s+/);
    if (columns.length < 3) continue;
    const name = columns[0];
    const status = columns[2];
    if (!name || !status || name === "NAME") continue;
    sandboxes.push({ name, status });
  }
  return sandboxes;
}

/**
 * Microsandbox keeps the VM across restarts but only runs the container
 * entrypoint (`scripts.start`, which is what launches `opencode serve`) at
 * creation. After a VM restart the sandbox reports `running` with no backend
 * process listening, so a running VM is not the same as a ready backend.
 */
const GUEST_ENTRYPOINT = "/.msb/scripts/start";
const RELAUNCH_COMMAND = `nohup ${GUEST_ENTRYPOINT} >/var/log/opencode.log 2>&1 &`;

export class MicrosandboxRuntime implements RuntimeAdapter {
  readonly name = "microsandbox" as const;

  constructor(private readonly runner: CommandRunner) {}

  async isRunning(config: Config): Promise<boolean> {
    const sandbox = await this.findSandbox(config);
    return sandbox?.status === "running";
  }

  async start(config: Config): Promise<void> {
    const apiKey = requireAlbertApiKey(config);
    await mkdir(config.projectDir, { recursive: true });
    const env = { ALBERT_API_KEY: apiKey };

    const sandbox = await this.findSandbox(config);
    if (sandbox?.status === "running") {
      const endpoint = this.endpoint(config);
      if (await isHealthy(endpoint, config)) {
        console.log(`${config.msbSandbox} is already running with a healthy backend.`);
        return;
      }
      console.log(
        `${config.msbSandbox} is running but its backend is not responding; ` +
          "restarting OpenCode inside the microVM...",
      );
      await this.runner.checked("msb", [
        "exec",
        config.msbSandbox,
        "--",
        "sh",
        "-c",
        RELAUNCH_COMMAND,
      ], { env });
      return;
    }

    if (sandbox) {
      console.log(`Starting ${config.msbSandbox}...`);
      await this.runner.checked("msb", [
        "modify",
        config.msbSandbox,
        "--env",
        `OPENCODE_SERVER_PASSWORD=${config.opencodePassword}`,
        "--env",
        `OPENCODE_SERVER_USERNAME=${config.opencodeUsername}`,
        "--next-start",
      ], { env });
      const result = await this.runner.checked("msb", ["start", config.msbSandbox], { env });
      printCommandOutput(result.stdout);
      return;
    }

    const assets = await materializeAssets(config);
    console.log(`Creating ${config.msbSandbox} microVM...`);
    console.log(
      "First start installs the toolchain inside the microVM (build-base, node, python); " +
        "this can take several minutes.",
    );
    const result = await this.runner.checked("msb", [
      "run",
      "--name",
      config.msbSandbox,
      "--detach",
      "--conf",
      assets.microsandboxConfig,
      "--root-disk",
      "8G",
      "--volume",
      `${config.projectDir}:/workspace`,
      "--env",
      `OPENCODE_SERVER_PASSWORD=${config.opencodePassword}`,
      "--env",
      `OPENCODE_SERVER_USERNAME=${config.opencodeUsername}`,
      config.msbImage,
    ], { env });
    printCommandOutput(result.stdout);
  }

  async stop(config: Config): Promise<void> {
    if (!(await this.isRunning(config))) {
      console.log(`${config.msbSandbox} is not running.`);
      return;
    }
    console.log(`Stopping ${config.msbSandbox}...`);
    const result = await this.runner.checked("msb", ["stop", "--timeout", "3", config.msbSandbox]);
    printCommandOutput(result.stdout);
  }

  async build(config: Config): Promise<void> {
    await foregroundChecked(this.runner, "msb", ["pull", config.msbImage]);
  }

  async restart(config: Config): Promise<void> {
    await this.clean(config);
    await this.start(config);
  }

  async logs(config: Config): Promise<void> {
    await foregroundChecked(this.runner, "msb", ["logs", "--follow", config.msbSandbox]);
  }

  async shell(config: Config): Promise<void> {
    await foregroundChecked(this.runner, "msb", ["exec", config.msbSandbox, "--", "/bin/bash"]);
  }

  async clean(config: Config): Promise<void> {
    if (!(await this.findSandbox(config))) {
      console.log(`${config.msbSandbox} does not exist.`);
      return;
    }
    await foregroundChecked(this.runner, "msb", ["rm", "--force", config.msbSandbox]);
  }

  async doctor(): Promise<void> {
    await foregroundChecked(this.runner, "msb", ["doctor"]);
  }

  async getEndpoint(config: Config): Promise<string> {
    return this.endpoint(config);
  }

  private endpoint(config: Config): string {
    return `http://localhost:${config.port}`;
  }

  private async findSandbox(config: Config): Promise<MsbSandbox | undefined> {
    if (!this.runner.commandExists("msb")) return undefined;
    const result = await this.runner.run("msb", ["ls"]);
    if (result.exitCode !== 0) return undefined;
    return parseMsbLs(result.stdout).find((sandbox) => sandbox.name === config.msbSandbox);
  }
}
