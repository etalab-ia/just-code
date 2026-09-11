import { mkdir } from "node:fs/promises";

import { materializeAssets } from "../assets.ts";
import { requireAlbertApiKey } from "../config.ts";
import type { CommandRunner } from "../process.ts";
import type { Config, RuntimeAdapter } from "../types.ts";
import { foregroundChecked, printCommandOutput } from "./shared.ts";

const CONTAINER_NAME = "albert-opencode-sandbox";
const COMPOSE_PROJECT = "just-code";

export class DockerRuntime implements RuntimeAdapter {
  readonly name = "docker" as const;

  constructor(private readonly runner: CommandRunner) {}

  async isRunning(): Promise<boolean> {
    if (!this.runner.commandExists("docker")) return false;
    const result = await this.runner.run("docker", ["container", "inspect", "--format={{.State.Running}}", CONTAINER_NAME]);
    return result.exitCode === 0 && result.stdout.trim() === "true";
  }

  async start(config: Config): Promise<void> {
    const apiKey = requireAlbertApiKey(config);
    await mkdir(config.projectDir, { recursive: true });
    const assets = await materializeAssets(config);
    console.log("Starting the Docker sandbox (the first run builds the image)...");
    const result = await this.runner.checked("docker", [
      "compose",
      "--project-name",
      COMPOSE_PROJECT,
      "--project-directory",
      assets.directory,
      "--file",
      assets.dockerCompose,
      "up",
      "--detach",
      "--quiet-pull",
    ], { env: this.environment(config, apiKey) });
    printCommandOutput(result.stdout);
  }

  async stop(config: Config): Promise<void> {
    const assets = await materializeAssets(config);
    const result = await this.runner.checked("docker", [
      "compose",
      "--project-name",
      COMPOSE_PROJECT,
      "--project-directory",
      assets.directory,
      "--file",
      assets.dockerCompose,
      "down",
      "--timeout",
      "3",
    ], { env: this.environment(config) });
    printCommandOutput(result.stdout);
  }

  async build(config: Config): Promise<void> {
    const assets = await materializeAssets(config);
    await foregroundChecked(this.runner, "docker", [
      "compose",
      "--project-name",
      COMPOSE_PROJECT,
      "--project-directory",
      assets.directory,
      "--file",
      assets.dockerCompose,
      "build",
      "--quiet",
    ], { env: this.environment(config) });
  }

  async restart(config: Config): Promise<void> {
    await this.stop(config);
    await this.build(config);
    await this.start(config);
  }

  async logs(config: Config): Promise<void> {
    const assets = await materializeAssets(config);
    await foregroundChecked(this.runner, "docker", [
      "compose",
      "--project-name",
      COMPOSE_PROJECT,
      "--project-directory",
      assets.directory,
      "--file",
      assets.dockerCompose,
      "logs",
      "--follow",
    ], { env: this.environment(config) });
  }

  async shell(): Promise<void> {
    await foregroundChecked(this.runner, "docker", ["exec", "-it", CONTAINER_NAME, "bash"]);
  }

  async clean(config: Config): Promise<void> {
    const assets = await materializeAssets(config);
    await foregroundChecked(this.runner, "docker", [
      "compose",
      "--project-name",
      COMPOSE_PROJECT,
      "--project-directory",
      assets.directory,
      "--file",
      assets.dockerCompose,
      "down",
      "--timeout",
      "3",
      "--rmi",
      "local",
    ], { env: this.environment(config) });
  }

  async doctor(): Promise<void> {
    await foregroundChecked(this.runner, "docker", ["info"]);
    await foregroundChecked(this.runner, "docker", ["compose", "version"]);
    console.log("Docker runtime is ready.");
  }

  async getEndpoint(config: Config): Promise<string> {
    return `http://localhost:${config.port}`;
  }

  private environment(config: Config, apiKey = config.albertApiKey): Record<string, string> {
    return {
      ALBERT_API_KEY: apiKey ?? "",
      OPENCODE_SERVER_PASSWORD: config.opencodePassword,
      OPENCODE_SERVER_USERNAME: config.opencodeUsername,
      PROJECT_DIR: config.projectDir,
    };
  }
}
