import { NextResponse } from "next/server";
export const jsonError = (message: string, status: number, details?: unknown) => NextResponse.json({ error: message, ...(details ? { details } : {}) }, { status });
export function pageParams(search: URLSearchParams) {
  const page = Math.max(1, Number(search.get("page") || 1));
  const pageSize = Math.min(100, Math.max(1, Number(search.get("pageSize") || 20)));
  return { page, pageSize, skip: (page - 1) * pageSize };
}
