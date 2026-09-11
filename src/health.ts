import { CliError, errorMessage } from "./errors.ts";
import { sleep } from "./runtimes/shared.ts";
import type { Config } from "./types.ts";

function authorization(config: Config): string {
  const credentials = Buffer.from(
    `${config.opencodeUsername}:${config.opencodePassword}`,
    "utf8",
  ).toString("base64");
  return `Basic ${credentials}`;
}

export async function requestJson(
  url: string,
  config: Config,
  timeoutMs = 5_000,
): Promise<unknown> {
  const response = await fetch(url, {
    headers: { Authorization: authorization(config) },
    signal: AbortSignal.timeout(timeoutMs),
  });
  if (!response.ok) throw new CliError(`${url} returned HTTP ${response.status}.`);
  return response.json();
}

function reportsHealthy(payload: unknown): boolean {
  if (typeof payload === "string") return payload.includes("healthy");
  if (!payload || typeof payload !== "object") return false;
  const report = payload as Record<string, unknown>;
  return report.healthy === true || report.healthy === "healthy" || report.status === "healthy";
}

export async function isHealthy(endpoint: string, config: Config): Promise<boolean> {
  try {
    return reportsHealthy(await requestJson(`${endpoint}/global/health`, config));
  } catch {
    return false;
  }
}

export interface WaitOptions {
  timeoutMs?: number;
  intervalMs?: number;
  progressIntervalMs?: number;
  onProgress?: (elapsedMs: number, timeoutMs: number) => void;
}

const DEFAULT_PROGRESS_INTERVAL_MS = 15_000;

export async function waitForHealth(
  endpoint: string,
  config: Config,
  options: WaitOptions = {},
): Promise<void> {
  const timeoutMs = options.timeoutMs ?? config.startTimeoutMs;
  const intervalMs = options.intervalMs ?? 500;
  const progressIntervalMs = options.progressIntervalMs ?? DEFAULT_PROGRESS_INTERVAL_MS;
  const startedAt = Date.now();
  const deadline = startedAt + timeoutMs;
  let lastReportedAt = startedAt;

  while (Date.now() < deadline) {
    if (await isHealthy(endpoint, config)) return;

    const now = Date.now();
    if (options.onProgress && now - lastReportedAt >= progressIntervalMs) {
      lastReportedAt = now;
      options.onProgress(now - startedAt, timeoutMs);
    }
    await sleep(Math.min(intervalMs, Math.max(0, deadline - Date.now())));
  }

  throw new CliError(
    `Backend did not become healthy within ${Math.ceil(timeoutMs / 1_000)}s.`,
  );
}

export async function printHealthReport(endpoint: string, config: Config): Promise<void> {
  try {
    const health = await requestJson(`${endpoint}/global/health`, config);
    console.log(JSON.stringify(health));
    const payload = await requestJson(`${endpoint}/provider`, config);
    if (!payload || typeof payload !== "object") throw new CliError("Provider response is not an object.");
    const provider = payload as { all?: unknown; default?: unknown };
    const all = Array.isArray(provider.all) ? provider.all : [];
    const registered = all.some(
      (entry) => entry && typeof entry === "object" && "id" in entry && entry.id === "albert",
    );
    const defaults = provider.default && typeof provider.default === "object"
      ? provider.default as Record<string, unknown>
      : {};
    const defaultModel = defaults.albert === undefined ? "unknown" : String(defaults.albert);
    console.log(`albert provider: ${registered ? `registered, default ${defaultModel}` : "MISSING"}`);
  } catch (error) {
    throw new CliError(`Health check failed: ${errorMessage(error)}`);
  }
}
