import { OnlinePlatform, PrismaClient, RoomKind } from "@prisma/client";
import { hash } from "bcryptjs";
import { syncZoomBreakoutRooms } from "../src/lib/zoom-breakouts";

const prisma = new PrismaClient();

async function main() {
  for (const [id, name, shortName] of [["dept-pra-konstruksi", "Pra Konstruksi", "PRA"], ["dept-operasi-1", "Operasi Konstruksi 1", "OPK 1"], ["dept-operasi-2", "Operasi Konstruksi 2", "OPK 2"], ["dept-keuangan", "Keuangan, Umum & SDM", "KUSD"], ["dept-csr-umum", "CSR & Umum", "CSR"]]) {
    await prisma.department.upsert({ where: { name }, update: {}, create: { id, name, shortName } });
  }
  for (const room of [
    { id: "room-uip-1", name: "R. Rapat Lt. 1", capacity: 20, location: "Gedung UIP", facilities: ["LCD", "Mic", "WiFi"] },
    { id: "room-uip-2", name: "R. Rapat Lt. 2", capacity: 16, location: "Gedung UIP", facilities: ["LCD", "Mic", "WiFi"] },
    { id: "room-uip-3", name: "R. Rapat Lt. 3", capacity: 25, location: "Gedung UIP", facilities: ["LCD", "Mic", "WiFi"] },
    { id: "room-uip-4", name: "R. Serbaguna Lt. 3", capacity: 50, location: "Gedung UIP", facilities: ["LCD", "Mic", "WiFi", "Stage"] },
    { id: "room-uip-zoom", name: "Zoom Meeting Utama", capacity: 300, location: "Online", facilities: ["Video Conference"], kind: RoomKind.ONLINE, onlinePlatform: OnlinePlatform.ZOOM, breakoutRooms: 10 },
    { id: "room-uip-teams", name: "Microsoft Teams Meeting", capacity: 300, location: "Online", facilities: ["Video Conference", "Teams breakout rooms"], kind: RoomKind.ONLINE, onlinePlatform: OnlinePlatform.TEAMS, breakoutRooms: 50 },
  ]) {
    await prisma.room.upsert({ where: { id: room.id }, update: room, create: room });
  }
  const zoomRooms = await prisma.room.findMany({ where: { kind: RoomKind.ONLINE, onlinePlatform: OnlinePlatform.ZOOM } });
  for (const room of zoomRooms) await prisma.$transaction(tx => syncZoomBreakoutRooms(tx, room.id, room.capacity, room.breakoutRooms));
  const department = await prisma.department.findUniqueOrThrow({ where: { name: "Pra Konstruksi" } });
  const csrDepartment = await prisma.department.findUniqueOrThrow({ where: { name: "CSR & Umum" } });
  const passwordHash = await hash(process.env.DEMO_PASSWORD ?? "UIPDemo2026!", 12);
  await prisma.user.upsert({
    where: { email: "admin@uip.local" },
    update: { passwordHash, role: "ADMIN", departmentId: department.id },
    create: { id: "demo-admin", name: "Administrator UIP", email: "admin@uip.local", passwordHash, role: "ADMIN", departmentId: department.id },
  });
  await prisma.user.upsert({
    where: { email: "approver@uip.local" },
    update: { passwordHash, role: "APPROVER", departmentId: csrDepartment.id },
    create: { id: "demo-approver", name: "Approver UIP", email: "approver@uip.local", passwordHash, role: "APPROVER", departmentId: csrDepartment.id },
  });
  await prisma.systemSetting.upsert({ where: { key: "allowSelfApproval" }, update: {}, create: { key: "allowSelfApproval", value: false } });
  await prisma.announcement.upsert({
    where: { id: "announcement-welcome" }, update: {},
    create: { id: "announcement-welcome", title: "UIP Meeting Portal", body: "Gunakan portal ini untuk mengajukan dan mengelola rapat." },
  });
}

main().finally(() => prisma.$disconnect());
