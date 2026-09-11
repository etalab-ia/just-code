import { describe, expect, test } from "bun:test";

import { loadConfig } from "../src/config.ts";
import { CliError } from "../src/errors.ts";
import { RuntimeManager } from "../src/manager.ts";
import type { CommandOptions, CommandResult, CommandRunner } from "../src/process.ts";
import type { Config, RuntimeAdapter, RuntimeName } from "../src/types.ts";

class FakeRunner implements CommandRunner {
  readonly foregroundCalls: string[] = [];

  async run(): Promise<CommandResult> {
    return { exitCode: 0, stdout: "", stderr: "" };
  }

  async checked(): Promise<CommandResult> {
    return { exitCode: 0, stdout: "", stderr: "" };
  }

  async foreground(command: string, args?: readonly string[]): Promise<number> {
    this.foregroundCalls.push([command, ...(args ?? [])].join(" "));
    return 0;
  }

  async detached(): Promise<number> {
    return 1;
  }

  commandExists(): boolean {
    return true;
  }
}

class FakeRuntime implements RuntimeAdapter {
  readonly calls: string[] = [];

  constructor(
    readonly name: RuntimeName,
    public running = false,
  ) {}

  async isRunning(): Promise<boolean> {
    return this.running;
  }

  async start(): Promise<void> {
    this.calls.push("start");
    this.running = true;
  }

  async stop(): Promise<void> {
    this.calls.push("stop");
    this.running = false;
  }

  async build(): Promise<void> { this.calls.push("build"); }
  async restart(): Promise<void> { this.calls.push("restart"); }
  async logs(): Promise<void> { this.calls.push("logs"); }
  async shell(): Promise<void> { this.calls.push("shell"); }
  async clean(): Promise<void> { this.calls.push("clean"); }
  async doctor(): Promise<void> { this.calls.push("doctor"); }
  async getEndpoint(config: Config): Promise<string> { return `http://localhost:${config.port}`; }
}

function setup(running: RuntimeName[] = [], accepted = true, interactive = true, healthFails = false) {
  const docker = new FakeRuntime("docker", running.includes("docker"));
  const microsandbox = new FakeRuntime("microsandbox", running.includes("microsandbox"));
  const tart = new FakeRuntime("tart", running.includes("tart"));
  const answers: string[] = [];
  const runner = new FakeRunner();
  const manager = new RuntimeManager(
    runner,
    async (question) => {
      answers.push(question);
      return accepted;
    },
    {
      adapters: { docker, microsandbox, tart },
      isInteractive: () => interactive,
      waitForHealth: async () => {
        if (healthFails) throw new CliError("Backend did not become healthy within 300s.");
      },
    },
  );
  const config = loadConfig({ HOME: "/home/test", ALBERT_API_KEY: "secret" }, "/project");
  return { manager, config, docker, microsandbox, tart, answers, runner };
}

describe("runtime manager", () => {
  test("dispatches runtime actions", async () => {
    const { manager, config, docker } = setup();
    await manager.execute("build", "docker", config);
    expect(docker.calls).toEqual(["build"]);
  });

  test("uses the configured runtime when no explicit flag is supplied", async () => {
    const { manager, config, microsandbox } = setup();
    await manager.execute("doctor", undefined, { ...config, runtime: "microsandbox" });
    expect(microsandbox.calls).toEqual(["doctor"]);
  });

  test("bare invocation without a selected runtime reports the selection error", async () => {
    const { manager, config } = setup();
    await expect(manager.execute("code", undefined, config)).rejects.toThrow(
      "Select --docker, --microsandbox, or --tart",
    );
  });

  test("stops an accepted conflicting runtime before start", async () => {
    const { manager, config, docker, tart, answers } = setup(["docker"]);
    await manager.execute("start", "tart", config);
    expect(docker.calls).toEqual(["stop"]);
    expect(tart.calls).toEqual(["start"]);
    expect(answers[0]).toContain("docker is already running");
  });

  test("keeps a rejected conflicting runtime", async () => {
    const { manager, config, docker, tart } = setup(["docker"], false);
    await expect(manager.execute("start", "tart", config)).rejects.toThrow("Keeping docker running");
    expect(docker.calls).toEqual([]);
    expect(tart.calls).toEqual([]);
  });

  test("non-interactive conflicts produce an actionable error", async () => {
    const { manager, config } = setup(["microsandbox"], true, false);
    await expect(manager.execute("start", "docker", config)).rejects.toThrow(
      "Run 'just-code stop' before starting docker",
    );
  });

  test("stop without a runtime stops every active adapter", async () => {
    const { manager, config, docker, tart } = setup(["docker", "tart"]);
    await manager.execute("stop", undefined, config);
    expect(docker.calls).toEqual(["stop"]);
    expect(tart.calls).toEqual(["stop"]);
  });

  test("attaches the TUI and asks about cleanup after a healthy start", async () => {
    const { manager, config, runner, answers } = setup();
    const code = await manager.execute("code", "docker", config);
    expect(code).toBe(0);
    expect(runner.foregroundCalls[0]).toStartWith("opencode attach http://localhost:4096");
    expect(answers[0]).toBe("Stop the docker runtime?");
  });

  test("reports a health failure without prompting or attaching", async () => {
    const { manager, config, docker, runner, answers } = setup([], true, true, true);
    const code = await manager.execute("code", "docker", config);
    expect(code).toBe(1);
    expect(runner.foregroundCalls).toEqual([]);
    expect(answers).toEqual([]);
    expect(docker.calls).toEqual(["start"]);
  });
});
