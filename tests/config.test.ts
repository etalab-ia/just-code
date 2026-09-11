import { describe, expect, test } from "bun:test";

import { loadConfig, tartVmName, validateTartMtu } from "../src/config.ts";
import { CliError } from "../src/errors.ts";

describe("configuration", () => {
  test("loads defaults and resolves workspace from the current directory", () => {
    const config = loadConfig({ HOME: "/home/test" }, "/project");
    expect(config.projectDir).toBe("/project/workspace");
    expect(config.stateDir).toBe("/home/test/.local/state/just-code");
    expect(config.opencodePassword).toBe("albert-dev-pass");
    expect(config.tartMtu).toBe("1280");
    expect(config.runtime).toBeUndefined();
  });

  test("preserves an explicitly empty OpenCode password", () => {
    const config = loadConfig({ HOME: "/home/test", OPENCODE_SERVER_PASSWORD: "" }, "/project");
    expect(config.opencodePassword).toBe("");
  });

  test("expands home paths and accepts a valid runtime", () => {
    const config = loadConfig({ HOME: "/home/test", PROJECT_DIR: "~/code", RUNTIME: "tart" }, "/project");
    expect(config.projectDir).toBe("/home/test/code");
    expect(config.runtime).toBe("tart");
  });

  test("rejects unknown runtimes", () => {
    expect(() => loadConfig({ HOME: "/home/test", RUNTIME: "podman" }, "/project"))
      .toThrow(new CliError("RUNTIME must be docker, microsandbox, or tart.", 2));
  });

  test("can ignore an invalid runtime for commands that do not select one", () => {
    const config = loadConfig({ HOME: "/home/test", RUNTIME: "podman" }, "/project", {
      validateRuntime: false,
    });
    expect(config.runtime).toBeUndefined();
  });

  test("skips start-timeout validation when the command never waits for health", () => {
    // A typo must not block `stop`, `clean`, `logs`, `doctor` or `check`.
    const config = loadConfig(
      { HOME: "/home/test", JUST_CODE_START_TIMEOUT: "600s" },
      "/project",
      { validateStartTimeout: false },
    );
    expect(config.startTimeoutMs).toBe(300_000);

    expect(() =>
      loadConfig({ HOME: "/home/test", JUST_CODE_START_TIMEOUT: "600s" }, "/project"),
    ).toThrow("JUST_CODE_START_TIMEOUT");
  });

  test("defaults the start timeout to five minutes and accepts an override", () => {
    expect(loadConfig({ HOME: "/home/test" }, "/project").startTimeoutMs).toBe(300_000);
    expect(
      loadConfig({ HOME: "/home/test", JUST_CODE_START_TIMEOUT: "600" }, "/project").startTimeoutMs,
    ).toBe(600_000);
  });

  test("rejects invalid start timeouts", () => {
    for (const value of ["0", "-5", "abc", "1.5"]) {
      expect(() => loadConfig({ HOME: "/home/test", JUST_CODE_START_TIMEOUT: value }, "/project"))
        .toThrow("JUST_CODE_START_TIMEOUT");
    }
  });

  test("treats an empty start timeout as unset", () => {
    expect(
      loadConfig({ HOME: "/home/test", JUST_CODE_START_TIMEOUT: "" }, "/project").startTimeoutMs,
    ).toBe(300_000);
  });
});

describe("Tart configuration", () => {
  test("derives stable managed VM names", () => {
    expect(tartVmName("ghcr.io/cirruslabs/macos-tahoe-base:latest"))
      .toBe("opencode-tahoe-base-latest");
    expect(tartVmName("registry.example/base@sha256:abc123"))
      .toBe("opencode-base-sha256-abc123");
  });

  test("validates MTU values", () => {
    for (const value of ["auto", "1280", "1400", "1500"]) expect(validateTartMtu(value)).toBe(value);
    for (const value of ["", "1279", "1501", "01280", "abc", "1280;id"]) {
      expect(() => validateTartMtu(value)).toThrow("TART_MTU must be");
    }
  });
});
