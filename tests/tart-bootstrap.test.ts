import { afterEach, describe, expect, test } from "bun:test";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const BOOTSTRAP = join(import.meta.dir, "..", "src", "embedded", "tart-bootstrap.sh.txt");
const temporaryDirectories: string[] = [];

interface BootstrapOptions {
  mtu?: string;
  route?: string;
  sudoStatus?: number;
}

async function command(directory: string, name: string, body: string): Promise<void> {
  const path = join(directory, name);
  await writeFile(path, `#!/bin/sh\n${body}\n`);
  await chmod(path, 0o755);
}

async function runBootstrap(options: BootstrapOptions = {}) {
  const directory = await mkdtemp(join(tmpdir(), "just-code-bootstrap-"));
  temporaryDirectories.push(directory);
  await Promise.all([
    command(directory, "route", `printf '%s\\n' '${options.route ?? "interface: en7"}'`),
    command(directory, "sudo", `echo "sudo:$*"; exit ${options.sudoStatus ?? 0}`),
    command(directory, "opencode", 'echo "opencode:$*"'),
    command(directory, "git", "exit 0"),
  ]);
  const args = ["/bin/sh", BOOTSTRAP, "4096", "test-user"];
  if (options.mtu !== undefined) args.push(options.mtu);
  const subprocess = Bun.spawn(args, {
    env: { ...process.env, HOME: directory, PATH: `${directory}:/usr/bin:/bin` },
    stdin: "pipe",
    stdout: "pipe",
    stderr: "pipe",
  });
  subprocess.stdin.write("test-password\ntest-key\n");
  subprocess.stdin.end();
  const [exitCode, stdout, stderr] = await Promise.all([
    subprocess.exited,
    new Response(subprocess.stdout).text(),
    new Response(subprocess.stderr).text(),
  ]);
  return { exitCode, stdout, stderr };
}

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

describe("Tart guest bootstrap", () => {
  test("applies default and numeric MTU values before starting OpenCode", async () => {
    for (const mtu of [undefined, "1280", "1400", "1500"]) {
      const result = await runBootstrap({ ...(mtu === undefined ? {} : { mtu }) });
      expect(result.exitCode).toBe(0);
      const expected = `sudo:-n ifconfig en7 mtu ${mtu ?? "1280"}`;
      expect(result.stdout).toContain(expected);
      expect(result.stdout.indexOf(expected)).toBeLessThan(result.stdout.indexOf("opencode:serve"));
    }
  });

  test("auto skips network configuration", async () => {
    const result = await runBootstrap({ mtu: "auto", route: "", sudoStatus: 1 });
    expect(result.exitCode).toBe(0);
    expect(result.stdout).not.toContain("sudo:");
  });

  test("rejects invalid MTU values before server start", async () => {
    for (const mtu of ["", "1279", "1501", "9000", "01280", "abc", "1280;id"]) {
      const result = await runBootstrap({ mtu });
      expect(result.exitCode).not.toBe(0);
      expect(result.stderr).toContain("TART_MTU must be");
      expect(result.stdout).not.toContain("opencode:");
    }
  });

  test("fails when the default route cannot be determined", async () => {
    const result = await runBootstrap({ route: "" });
    expect(result.exitCode).not.toBe(0);
    expect(result.stderr).toContain("Cannot determine");
  });

  test("fails before server start when MTU configuration is rejected", async () => {
    const result = await runBootstrap({ sudoStatus: 1 });
    expect(result.exitCode).not.toBe(0);
    expect(result.stderr).toContain("Cannot apply TART_MTU");
    expect(result.stdout).not.toContain("opencode:");
  });
});
