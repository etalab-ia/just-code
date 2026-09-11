import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

import dockerfile from "./embedded/Dockerfile.txt" with { type: "text" };
import dockerCompose from "./embedded/docker-compose.yaml" with { type: "text" };
import microsandboxConfig from "./embedded/microsandbox.yaml" with { type: "text" };
import tartBootstrap from "./embedded/tart-bootstrap.sh.txt" with { type: "text" };

import type { Config } from "./types.ts";

export interface MaterializedAssets {
  directory: string;
  dockerfile: string;
  dockerCompose: string;
  microsandboxConfig: string;
  tartDirectory: string;
  tartBootstrap: string;
}

export async function materializeAssets(config: Config): Promise<MaterializedAssets> {
  const directory = join(config.stateDir, "assets");
  const tartDirectory = join(config.stateDir, "tart");
  const paths: MaterializedAssets = {
    directory,
    dockerfile: join(directory, "Dockerfile"),
    dockerCompose: join(directory, "docker-compose.yaml"),
    microsandboxConfig: join(directory, "microsandbox.yaml"),
    tartDirectory,
    tartBootstrap: join(tartDirectory, "tart-bootstrap.sh"),
  };
  await Promise.all([
    mkdir(directory, { recursive: true }),
    mkdir(tartDirectory, { recursive: true }),
  ]);
  await Promise.all([
    writeFile(paths.dockerfile, dockerfile, { mode: 0o644 }),
    writeFile(paths.dockerCompose, dockerCompose, { mode: 0o644 }),
    writeFile(paths.microsandboxConfig, microsandboxConfig, { mode: 0o644 }),
    writeFile(paths.tartBootstrap, tartBootstrap, { mode: 0o644 }),
  ]);
  return paths;
}
