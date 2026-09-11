import { describe, expect, test } from "bun:test";

import { parseArguments } from "../src/arguments.ts";

describe("CLI argument parsing", () => {
  test("accepts runtime flags before or after the command", () => {
    expect(parseArguments(["start", "--tart"])).toEqual({ action: "start", runtime: "tart", version: false });
    expect(parseArguments(["--docker", "start"])).toEqual({ action: "start", runtime: "docker", version: false });
  });

  test("defaults to starting and attaching with no command", () => {
    expect(parseArguments([])).toEqual({ action: "code", version: false });
    expect(parseArguments(["--tart"])).toEqual({ action: "code", runtime: "tart", version: false });
  });

  test("still exposes help explicitly", () => {
    expect(parseArguments(["--help"])).toEqual({ action: "help", version: false });
    expect(parseArguments(["help"])).toEqual({ action: "help", version: false });
  });

  test("points former 'code' users at the bare invocation", () => {
    expect(() => parseArguments(["code"])).toThrow("run 'just-code' with no command");
  });

  test("rejects conflicting runtimes and unknown arguments", () => {
    expect(() => parseArguments(["start", "--tart", "--docker"])).toThrow("Select exactly one runtime");
    expect(() => parseArguments(["launch"])).toThrow("Unknown argument: launch");
  });
});
