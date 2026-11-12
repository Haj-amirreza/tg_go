import type { components } from "./api.generated";
export type SystemStatus = components["schemas"]["SystemStatus"];
export async function getSystemStatus(): Promise<SystemStatus> {
  const response = await fetch("/api/v1/system/status", {
    signal: AbortSignal.timeout(5000),
    credentials: "same-origin",
  });
  if (!response.ok) throw new Error("status_unavailable");
  const value: unknown = await response.json();
  if (typeof value !== "object" || value === null)
    throw new Error("invalid_status");
  const v = value as Record<string, unknown>;
  if (
    v.service !== "university-bot" ||
    v.stage !== 4 ||
    !["development", "test", "production"].includes(String(v.environment)) ||
    typeof v.databaseReady !== "boolean" ||
    typeof v.authenticationReady !== "boolean" ||
    typeof v.botConnected !== "boolean"
  )
    throw new Error("invalid_status");
  return value as SystemStatus;
}
