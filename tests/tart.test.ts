import { describe, expect, test } from "bun:test";

import { parseTartList } from "../src/runtimes/tart.ts";

describe("Tart VM discovery", () => {
  test("parses local VM names and states while ignoring headers and remote entries", () => {
    const output = `Source  Name                          Disk Size  Size on Disk  State\nlocal   opencode-tahoe-base-latest   80 GB      21 GB         running\nlocal   personal-dev                 80 GB      18 GB         stopped\nremote  team-image                   80 GB      10 GB         running\n`;
    expect(parseTartList(output)).toEqual([
      { name: "opencode-tahoe-base-latest", state: "running" },
      { name: "personal-dev", state: "stopped" },
    ]);
  });
});
