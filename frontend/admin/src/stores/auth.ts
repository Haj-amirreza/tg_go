import { defineStore } from "pinia";
import { ref } from "vue";
import type { components } from "../lib/api.generated";
export type Actor = components["schemas"]["Actor"];
export type Role = components["schemas"]["Role"];
type LoginInput = components["schemas"]["LoginInput"];
type TelegramInput = components["schemas"]["TelegramInput"];
export class AuthError extends Error {
  constructor(public status: number) {
    super(
      status === 429
        ? "تلاش‌های ورود زیاد بوده است؛ کمی بعد دوباره امتحان کنید."
        : status === 401
          ? "اطلاعات ورود یا کد دومرحله‌ای معتبر نیست."
          : status === 403
            ? "اعتبار فرم تمام شده است؛ صفحه را تازه کنید."
            : "ارتباط با سرویس ورود برقرار نشد. دوباره تلاش کنید.",
    );
  }
}
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch("/api/v1/" + path, {
    ...init,
    credentials: "same-origin",
    signal: AbortSignal.timeout(10000),
  });
  if (!response.ok) throw new AuthError(response.status);
  return (await response.json()) as T;
}
export const useAuth = defineStore("auth", () => {
  const actor = ref<Actor | null>(null);
  async function load() {
    try {
      actor.value = await request<Actor>("admin/session");
    } catch (e) {
      actor.value = null;
      if (!(e instanceof AuthError && e.status === 401)) throw e;
    }
  }
  async function signIn(input: LoginInput | TelegramInput, mini = false) {
    const { csrf } = await request<components["schemas"]["CSRF"]>("auth/csrf");
    actor.value = await request<Actor>(mini ? "auth/telegram" : "auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify(input),
    });
  }
  async function logout() {
    if (actor.value)
      await request("admin/logout", {
        method: "POST",
        headers: { "X-CSRF-Token": actor.value.csrf },
      });
    actor.value = null;
  }
  function can(permission: string) {
    return (
      actor.value?.super ||
      actor.value?.permissions.includes(permission) ||
      false
    );
  }
  const roles = () => request<Role[]>("admin/roles");
  const performance = () =>
    request<components["schemas"]["Performance"]>("admin/performance");
  async function clearCache() {
    if (actor.value)
      await request("admin/cache/clear", {
        method: "POST",
        headers: { "X-CSRF-Token": actor.value.csrf },
      });
  }
  const queueJobs = (after = "0") =>
    request<components["schemas"]["QueueItem"][]>(
      "admin/queue/jobs?after=" + encodeURIComponent(after),
    );
  async function queueAction(path: string, value: unknown) {
    if (actor.value)
      await request("admin/queue/" + path, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": actor.value.csrf,
        },
        body: JSON.stringify(value),
      });
  }
  return {
    actor,
    load,
    signIn,
    logout,
    can,
    roles,
    performance,
    clearCache,
    queueJobs,
    queueAction,
  };
});
