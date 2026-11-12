export class DomainError extends Error {
  constructor(
    public status: number,
    public code: string,
  ) {
    super(
      status === 409
        ? "این رکورد تغییر کرده است؛ دوباره آن را باز کنید."
        : status === 403
          ? "اجازهٔ این عملیات را ندارید یا اعتبار فرم تمام شده است."
          : status === 400
            ? "اطلاعات فرم معتبر نیست؛ فیلدها را بررسی کنید."
            : status === 401
              ? "نشست شما پایان یافته؛ دوباره وارد شوید."
              : "ارتباط با سرویس برقرار نشد. دوباره تلاش کنید.",
    );
  }
}
export async function domainRequest<T>(
  path: string,
  method = "GET",
  body?: unknown,
  csrf = "",
  key = "",
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (csrf) headers["X-CSRF-Token"] = csrf;
  if (key) headers["Idempotency-Key"] = key;
  const response = await fetch("/api/v1/admin/" + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
    signal: AbortSignal.timeout(15000),
  });
  if (!response.ok) {
    const value = await response.json().catch(() => ({ code: "unavailable" }));
    throw new DomainError(response.status, String(value.code));
  }
  return (await response.json()) as T;
}
export const commandKey = () => crypto.randomUUID().replaceAll("-", "");
