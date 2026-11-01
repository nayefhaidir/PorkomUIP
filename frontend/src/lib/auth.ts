import { decodeJwt } from "jose";
import { cookies } from "next/headers";
import { GoApiError, goApiFetch } from "@/lib/go-api";

export const cookieName = "uip_session";

export type Role = "ADMIN" | "APPROVER" | "ORGANIZER" | "PARTICIPANT";

export type AuthenticatedUser = {
  id: string;
  name: string;
  email: string;
  role: Role;
  jobTitle: string | null;
  departmentId: string | null;
  pageAccess: string[];
  department: { id: string; name: string; shortName: string; color: string } | null;
};

export async function currentUser() {
  const token = (await cookies()).get(cookieName)?.value;
  if (!token) return null;

  try {
    const user = await goApiFetch<AuthenticatedUser>("auth/me");
    const sessionId = decodeJwt(token).sid;
    if (typeof sessionId !== "string" || !sessionId) throw new Error("Sesi Go API tidak valid");
    return { user, token, sessionId };
  } catch (error) {
    if (error instanceof GoApiError && error.status === 401) return null;
    throw error;
  }
}

export function hasPageAccess(user: Pick<AuthenticatedUser, "role" | "pageAccess">, page: string, fallback: boolean) {
  if (user.role === "ADMIN") return true;
  return user.pageAccess.includes("__custom__") ? user.pageAccess.includes(page) : fallback;
}
