import { afterEach, describe, expect, test } from "bun:test";

import { loadConfig } from "../src/config.ts";
import { isHealthy, requestJson, waitForHealth } from "../src/health.ts";

const servers: ReturnType<typeof Bun.serve>[] = [];

afterEach(() => {
  for (const server of servers.splice(0)) server.stop(true);
});

describe("health client", () => {
  test("sends Basic authentication and parses JSON", async () => {
    const state: { authorization: string | null } = { authorization: null };
    const server = Bun.serve({
      port: 0,
      fetch(request) {
        state.authorization = request.headers.get("authorization");
        return Response.json({ healthy: true });
      },
    });
    servers.push(server);
    const config = loadConfig({
      HOME: "/home/test",
      OPENCODE_SERVER_USERNAME: "test-user",
      OPENCODE_SERVER_PASSWORD: "p:a ss",
    });

    expect(await requestJson(`http://localhost:${server.port}/health`, config)).toEqual({ healthy: true });
    expect(state.authorization).toBe(`Basic ${Buffer.from("test-user:p:a ss").toString("base64")}`);
  });

  test("recognizes healthy responses and rejects unhealthy ones", async () => {
    let report: Record<string, unknown> = { healthy: false, database: true };
    const server = Bun.serve({
      port: 0,
      fetch() {
        return Response.json(report);
      },
    });
    servers.push(server);
    const config = loadConfig({ HOME: "/home/test" });
    const endpoint = `http://localhost:${server.port}`;

    expect(await isHealthy(endpoint, config)).toBe(false);
    report = { healthy: true };
    expect(await isHealthy(endpoint, config)).toBe(true);
  });

  test("reports elapsed progress while waiting, then fails with the configured timeout", async () => {
    const config = loadConfig({ HOME: "/home/test" });
    const progress: number[] = [];
    const started = Date.now();

    await expect(
      waitForHealth("http://127.0.0.1:1", config, {
        timeoutMs: 400,
        intervalMs: 20,
        progressIntervalMs: 80,
        onProgress: (elapsedMs) => progress.push(elapsedMs),
      }),
    ).rejects.toThrow("Backend did not become healthy within 1s.");

    expect(progress.length).toBeGreaterThan(0);
    expect(Date.now() - started).toBeLessThan(5_000);
  });

  test("returns as soon as the backend is healthy", async () => {
    const server = Bun.serve({
      port: 0,
      fetch() {
        return Response.json({ healthy: true });
      },
    });
    servers.push(server);
    const config = loadConfig({ HOME: "/home/test" });

    await waitForHealth(`http://localhost:${server.port}`, config, { timeoutMs: 2_000, intervalMs: 20 });
  });
});
