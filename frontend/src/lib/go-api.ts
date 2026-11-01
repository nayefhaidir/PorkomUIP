import { cookies } from "next/headers";

const apiBase = (process.env.GO_API_URL ?? "http://localhost:8080").replace(/\/$/, "");

export class GoApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "GoApiError";
  }
}

async function cookieHeader() {
  const jar = await cookies();
  return jar.getAll().map(({ name, value }) => `${name}=${value}`).join("; ");
}

export async function goApiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const cookie = await cookieHeader();
  if (cookie) headers.set("cookie", cookie);
  headers.set("accept", "application/json");

  let response: Response | undefined;
  try {
    const url = `${apiBase}/api/${path.replace(/^\/+/, "")}`;
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        response = await fetch(url, {
          ...init,
          headers,
          cache: "no-store",
        });
        break;
      } catch (error) {
        if (attempt === 2) throw error;
        await new Promise(resolve => setTimeout(resolve, 100 * (attempt + 1)));
      }
    }
  } catch (error) {
    throw new GoApiError(
      error instanceof Error ? `Go API tidak dapat dihubungi: ${error.message}` : "Go API tidak dapat dihubungi.",
      503,
    );
  }
  if (!response) throw new GoApiError("Go API tidak dapat dihubungi.", 503);

  if (!response.ok) {
    let message = `Go API gagal (${response.status})`;
    try {
      const body = await response.json() as { error?: unknown };
      if (typeof body.error === "string" && body.error.trim()) message = body.error;
    } catch {
      // Keep the HTTP status message when the server does not return JSON.
    }
    throw new GoApiError(message, response.status);
  }

  return await response.json() as T;
}

export async function goApiItems<T>(path: string, query: URLSearchParams = new URLSearchParams()): Promise<T[]> {
  const pageSize = 100;
  const items: T[] = [];
  let page = 1;
  let total = Number.POSITIVE_INFINITY;
  while (items.length < total) {
    const params = new URLSearchParams(query);
    params.set("page", String(page));
    params.set("pageSize", String(pageSize));
    const result = await goApiFetch<{ items: T[]; total: number }>(`${path}?${params}`);
    if (!Array.isArray(result.items) || !Number.isSafeInteger(result.total) || result.total < 0) {
      throw new GoApiError("Format daftar data dari Go API tidak valid.", 502);
    }
    items.push(...result.items);
    total = result.total;
    if (result.items.length === 0) break;
    page++;
  }
  return items;
}
