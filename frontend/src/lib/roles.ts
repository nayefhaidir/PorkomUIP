import type { Role } from "@/lib/auth";

export const managerRoles: Role[] = ["ADMIN"];
export const requestRoles: Role[] = ["ADMIN", "ORGANIZER"];
export function roleLabel(role: Role | string) {
  return ({ ADMIN: "Super User", APPROVER: "Senior Manager", ORGANIZER: "Sekretariat", PARTICIPANT: "User" } as Record<string, string>)[role] ?? role;
}
