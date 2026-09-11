import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { materializeAssets } from "../src/assets.ts";
import { loadConfig } from "../src/config.ts";
import type { CommandOptions, CommandResult, CommandRunner } from "../src/process.ts";
import { DockerRuntime } from "../src/runtimes/docker.ts";
import { MicrosandboxRuntime, parseMsbLs } from "../src/runtimes/microsandbox.ts";
import { TartRuntime } from "../src/runtimes/tart.ts";

const servers: ReturnType<typeof Bun.serve>[] = [];

class StubRunner implements CommandRunner {
  response: CommandResult = { exitCode: 0, stdout: "", stderr: "" };
  /** Per-command responses, keyed by the space-joined command line. */
  responses: Record<string, CommandResult> = {};
  readonly calls: { command: string; args: readonly string[]; options?: CommandOptions }[] = [];

  async run(command: string, args: readonly string[] = [], options?: CommandOptions): Promise<CommandResult> {
    this.calls.push({ command, args, ...(options === undefined ? {} : { options }) });
    return this.responses[[command, ...args].join(" ")] ?? this.response;
  }

  async checked(command: string, args: readonly string[] = [], options?: CommandOptions): Promise<CommandResult> {
    return this.run(command, args, options);
  }

  async foreground(command: string, args: readonly string[] = [], options?: CommandOptions): Promise<number> {
    this.calls.push({ command, args, ...(options === undefined ? {} : { options }) });
    return this.response.exitCode;
  }

  async detached(command: string, args: readonly string[], _logFile: string, options?: CommandOptions): Promise<number> {
    this.calls.push({ command, args, ...(options === undefined ? {} : { options }) });
    return 1;
  }

  commandExists(): boolean {
    return true;
  }
}

const MSB_LS_RUNNING = `NAME                       IMAGE                                STATUS     CREATED
albert-opencode-sandbox    ghcr.io/anomalyco/opencode:latest    running    2026-09-11 10:27:36
`;

const MSB_LS_STOPPED = `NAME                       IMAGE                                STATUS     CREATED
albert-opencode-sandbox    ghcr.io/anomalyco/opencode:latest    stopped    2026-09-11 10:27:36
`;

const temporaryDirectories: string[] = [];

afterEach(async () => {
  for (const server of servers.splice(0)) server.stop(true);
  await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

describe("embedded runtime assets", () => {
  test("materializes every file needed by standalone binaries", async () => {
    const home = await mkdtemp(join(tmpdir(), "just-code-assets-"));
    temporaryDirectories.push(home);
    const config = loadConfig({ HOME: home }, home);
    const assets = await materializeAssets(config);

    expect(await readFile(assets.dockerfile, "utf8")).toContain("FROM node:22-bookworm-slim");
    expect(await readFile(assets.dockerCompose, "utf8")).toContain("opencode-backend:");
    expect(await readFile(assets.microsandboxConfig, "utf8")).toContain("network:");
    expect(await readFile(assets.tartBootstrap, "utf8")).toContain("exec opencode serve");
    expect(assets.tartBootstrap.startsWith(assets.tartDirectory)).toBe(true);
  });
});

describe("runtime discovery adapters", () => {
  const config = loadConfig({ HOME: "/home/test" }, "/project");

  test("detects the managed Docker container only when it is running", async () => {
    const runner = new StubRunner();
    runner.response.stdout = "true\n";
    expect(await new DockerRuntime(runner).isRunning()).toBe(true);
    expect(runner.calls[0]?.args).toContain("albert-opencode-sandbox");
  });

  test("returns all running managed Tart VMs", async () => {
    const runner = new StubRunner();
    runner.response.stdout = `local opencode-tahoe-base-latest 80 20 running\nlocal opencode-sonoma-base-latest 80 20 stopped\nlocal personal-dev 80 20 running\n`;
    expect(await new TartRuntime(runner).runningVmNames()).toEqual(["opencode-tahoe-base-latest"]);
  });
});

describe("Microsandbox discovery", () => {
  const config = loadConfig({ HOME: "/home/test" }, "/project");

  test("parses name and status from msb ls output", () => {
    expect(parseMsbLs(MSB_LS_RUNNING)).toEqual([
      { name: "albert-opencode-sandbox", status: "running" },
    ]);
    expect(parseMsbLs(MSB_LS_STOPPED)).toEqual([
      { name: "albert-opencode-sandbox", status: "stopped" },
    ]);
    expect(parseMsbLs("")).toEqual([]);
  });

  test("treats the sandbox as running only when the VM state says so", async () => {
    const running = new StubRunner();
    running.responses["msb ls"] = { exitCode: 0, stdout: MSB_LS_RUNNING, stderr: "" };
    expect(await new MicrosandboxRuntime(running).isRunning(config)).toBe(true);

    const stopped = new StubRunner();
    stopped.responses["msb ls"] = { exitCode: 0, stdout: MSB_LS_STOPPED, stderr: "" };
    expect(await new MicrosandboxRuntime(stopped).isRunning(config)).toBe(false);

    const absent = new StubRunner();
    absent.responses["msb ls"] = { exitCode: 0, stdout: "NAME  IMAGE  STATUS  CREATED\n", stderr: "" };
    expect(await new MicrosandboxRuntime(absent).isRunning(config)).toBe(false);
  });
});

describe("Microsandbox start branches", () => {
  // A writable temp HOME keeps `mkdir(projectDir)` working outside root-run sandboxes.
  async function tempConfig() {
    const home = await mkdtemp(join(tmpdir(), "just-code-msb-branch-"));
    temporaryDirectories.push(home);
    return loadConfig({ HOME: home, ALBERT_API_KEY: "secret" }, home);
  }

  test("relaunches the backend inside a running VM whose backend is dead", async () => {
    const runner = new StubRunner();
    runner.responses["msb ls"] = { exitCode: 0, stdout: MSB_LS_RUNNING, stderr: "" };
    // Port 1 refuses connections, so the health probe fails fast.
    const config = { ...(await tempConfig()), port: 1 };

    await new MicrosandboxRuntime(runner).start(config);

    const exec = runner.calls.find((call) => call.args[0] === "exec");
    expect(exec?.args).toEqual([
      "exec",
      "albert-opencode-sandbox",
      "--",
      "sh",
      "-c",
      "nohup /.msb/scripts/start >/var/log/opencode.log 2>&1 &",
    ]);
    expect(runner.calls.some((call) => call.args[0] === "run")).toBe(false);
    expect(runner.calls.some((call) => call.args[0] === "start")).toBe(false);
  });

  test("does nothing when the VM is running and the backend is healthy", async () => {
    const server = Bun.serve({
      port: 0,
      fetch() {
        return Response.json({ healthy: true });
      },
    });
    servers.push(server);
    const runner = new StubRunner();
    runner.responses["msb ls"] = { exitCode: 0, stdout: MSB_LS_RUNNING, stderr: "" };
    const port = server.port;
    if (port === undefined) throw new Error("test server did not report a port");
    const config = { ...(await tempConfig()), port };

    await new MicrosandboxRuntime(runner).start(config);

    expect(runner.calls.some((call) => call.args[0] === "exec")).toBe(false);
    expect(runner.calls.some((call) => call.args[0] === "run")).toBe(false);
    expect(runner.calls.some((call) => call.args[0] === "start")).toBe(false);
  });

  test("starts an existing stopped sandbox and launches the backend inside it", async () => {
    const runner = new StubRunner();
    runner.responses["msb ls"] = { exitCode: 0, stdout: MSB_LS_STOPPED, stderr: "" };

    await new MicrosandboxRuntime(runner).start(await tempConfig());

    expect(runner.calls.some((call) => call.args[0] === "modify")).toBe(true);
    expect(runner.calls.some((call) => call.args[0] === "start")).toBe(true);
    expect(runner.calls.some((call) => call.args[0] === "run")).toBe(false);
    // Booting a stopped VM does not re-run the container entrypoint.
    expect(runner.calls.some((call) => call.args[0] === "exec")).toBe(true);
  });

  test("creates the sandbox when it is absent", async () => {
    const runner = new StubRunner();
    runner.responses["msb ls"] = { exitCode: 0, stdout: "NAME  IMAGE  STATUS  CREATED\n", stderr: "" };

    await new MicrosandboxRuntime(runner).start(await tempConfig());

    expect(runner.calls.some((call) => call.args[0] === "run")).toBe(true);
    expect(runner.calls.some((call) => call.args[0] === "start")).toBe(false);
    expect(runner.calls.some((call) => call.args[0] === "exec")).toBe(false);
  });
});

describe("runtime command construction", () => {
  test("uses a stable Docker Compose project for compatibility", async () => {
    const home = await mkdtemp(join(tmpdir(), "just-code-docker-"));
    temporaryDirectories.push(home);
    const config = loadConfig({ HOME: home }, home);
    const runner = new StubRunner();

    await new DockerRuntime(runner).build(config);

    expect(runner.calls[0]?.args).toContain("--project-name");
    expect(runner.calls[0]?.args).toContain("just-code");
  });

  test("passes the Albert key to Microsandbox through its environment", async () => {
    const home = await mkdtemp(join(tmpdir(), "just-code-msb-"));
    temporaryDirectories.push(home);
    const config = loadConfig({ HOME: home, ALBERT_API_KEY: "super-secret" }, home);
    const runner = new StubRunner();

    await new MicrosandboxRuntime(runner).start(config);

    expect(runner.calls.flatMap((call) => call.args)).not.toContain("super-secret");
    expect(runner.calls.at(-1)?.options?.env?.ALBERT_API_KEY).toBe("super-secret");
  });

  test("streams Tart secrets over stdin instead of command arguments", async () => {
    const home = await mkdtemp(join(tmpdir(), "just-code-tart-"));
    temporaryDirectories.push(home);
    const config = loadConfig({ HOME: home, ALBERT_API_KEY: "super-secret" }, home);
    const runner = new StubRunner();

    await new TartRuntime(runner).start(config);

    expect(runner.calls.flatMap((call) => call.args)).not.toContain("super-secret");
    const backend = runner.calls.find((call) => call.command === "tart" && call.args.includes("-i"));
    expect(backend?.options?.input).toBe(`${config.opencodePassword}\nsuper-secret\n`);
    const vm = runner.calls.find((call) => call.command === "tart" && call.args[0] === "run");
    expect(vm?.args.some((argument) => argument.includes(":ro"))).toBe(true);
  });

  test("rejects an invalid Tart MTU before invoking Tart", async () => {
    const config = {
      ...loadConfig({ HOME: "/home/test", ALBERT_API_KEY: "super-secret" }, "/project"),
      tartMtu: "1280;id",
    };
    const runner = new StubRunner();

    await expect(new TartRuntime(runner).start(config)).rejects.toThrow("TART_MTU must be");
    expect(runner.calls).toEqual([]);
  });
});
