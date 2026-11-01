import { PageFrame, PageTitle } from "@/app/components/page-frame";
import { CalendarDays, ChevronLeft, ChevronRight, Clock3, MapPin, UsersRound } from "lucide-react";
import { currentUser, hasPageAccess } from "@/lib/auth";
import { goApiFetch, goApiItems } from "@/lib/go-api";
import type { CSSProperties } from "react";

export const dynamic = "force-dynamic";
type MeetingMethod = "OFFLINE" | "ONLINE" | "HYBRID";
type MeetingStatus = "DRAFT" | "SUBMITTED" | "APPROVED" | "REJECTED" | "CANCELLED" | "COMPLETED";
type Department = { id: string; name: string; shortName: string; color: string };
type Location = { id: string; name: string; breakoutRooms?: number; onlinePlatform?: string } | null;
type CalendarMeeting = {
  id: string;
  title: string;
  description: string | null;
  startAt: Date;
  endAt: Date;
  status: MeetingStatus;
  method: MeetingMethod;
  isConfidential: boolean;
  expectedParticipants: number;
  breakoutRoomCount: number;
  externalLocation: string | null;
  notes: string | null;
  department: Department;
  room: Location;
  onlineResource: Location;
  requirements: { id: string; type: string }[];
  consumptions: { id: string; itemType: string; quantity: number; unit: string }[];
  breakoutRooms: { id: string; name: string; capacity: number }[];
  documents: { id: string; documentType: string; filename?: string }[];
  attendances: { id: string }[];
  organizer: { id: string; name: string; email: string };
};
const dateKey = (date: Date) => new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Jakarta", year: "numeric", month: "2-digit", day: "2-digit" }).format(date);
const timeLabel = (date: Date) => new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", hour: "2-digit", minute: "2-digit", hour12: false }).format(date);

export default async function CalendarPage({ searchParams }: { searchParams: Promise<Record<string, string | undefined>> }) {
  const auth = await currentUser();
  const canRequest = Boolean(auth&&hasPageAccess(auth.user,"pengajuan",auth.user.role==="ADMIN"||auth.user.role==="ORGANIZER"));
  const params = await searchParams;
  const currentMonth = new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Jakarta", year: "numeric", month: "2-digit" }).format(new Date());
  const selected = params.month && /^\d{4}-\d\d$/.test(params.month) ? params.month : currentMonth;
  const view = ["month", "week", "day", "agenda"].includes(params.view ?? "") ? params.view! : "month";
  const [year, month] = selected.split("-").map(Number);
  const from = new Date(Date.UTC(year, month - 1, 1) - 7 * 3600000);
  const to = new Date(Date.UTC(year, month, 1) - 7 * 3600000);
  const q = params.q?.trim();
  const departmentId = params.departmentId;
  const roomId = params.roomId;
  const method = ["OFFLINE", "ONLINE", "HYBRID"].includes(params.method ?? "") ? params.method as MeetingMethod : undefined;
  const range = new URLSearchParams({ from: from.toISOString(), to: new Date(to.getTime() - 1).toISOString() });
  let rawItems: Record<string, unknown>[];
  let departments: Department[];
  if (auth) {
    const [meetings, departmentsResult] = await Promise.all([
      goApiItems<Record<string, unknown>>("meetings", range),
      goApiFetch<Department[]>("departments"),
    ]);
    rawItems = meetings;
    departments = departmentsResult;
  } else {
    const publicQuery = new URLSearchParams(range);
    if (departmentId) publicQuery.set("departmentId", departmentId);
    if (method) publicQuery.set("method", method);
    if (roomId) publicQuery.set("roomId", roomId);
    if (q) publicQuery.set("q", q);
    const schedule = await goApiFetch<{ meetings: Record<string, unknown>[]; departments: Department[] }>(`public/schedule?${publicQuery}`);
    rawItems = schedule.meetings;
    departments = schedule.departments;
  }
  const allowedStatuses = auth ? new Set<MeetingStatus>(["SUBMITTED", "APPROVED", "COMPLETED"]) : new Set<MeetingStatus>(["APPROVED"]);
  const items = rawItems.map(item => ({
    ...item,
    startAt: new Date(String(item.startAt)),
    endAt: new Date(String(item.endAt)),
  } as unknown as CalendarMeeting))
    .filter(item => allowedStatuses.has(item.status))
    .filter(item => !departmentId || item.department.id === departmentId)
    .filter(item => !method || item.method === method)
    .filter(item => !roomId || item.room?.id === roomId || item.onlineResource?.id === roomId)
    .filter(item => !q || [item.title, item.department.name, item.room?.name, item.onlineResource?.name].some(value => value?.toLocaleLowerCase().includes(q.toLocaleLowerCase())))
    .sort((a, b) => a.startAt.getTime() - b.startAt.getTime());
  const mondayOffset = (new Date(Date.UTC(year, month - 1, 1)).getUTCDay() + 6) % 7;
  const dayCount = new Date(Date.UTC(year, month, 0)).getUTCDate();
  const cellCount = Math.ceil((mondayOffset + dayCount) / 7) * 7;
  const cells = Array.from({ length: cellCount }, (_, index) => new Date(Date.UTC(year, month - 1, 1 - mondayOffset + index, 12) - 7 * 3600000));
  const focusText = params.date && /^\d{4}-\d\d-\d\d$/.test(params.date) ? params.date : dateKey(new Date());
  const focus = new Date(focusText + "T12:00:00+07:00");
  const weekStart = new Date(Date.UTC(focus.getUTCFullYear(), focus.getUTCMonth(), focus.getUTCDate() - ((focus.getUTCDay() + 6) % 7), 12));
  const displayCells = view === "month" ? cells : view === "week" ? Array.from({ length: 7 }, (_, index) => new Date(weekStart.getTime() + index * 86400000)) : view === "day" ? [focus] : [];
  const monthLabel = new Intl.DateTimeFormat("id-ID", { month: "long", year: "numeric", timeZone: "UTC" }).format(new Date(Date.UTC(year, month - 1, 1, 12)));
  const changeMonth = (delta: number) => new Date(Date.UTC(year, month - 1 + delta, 1, 12)).toISOString().slice(0, 7);
  const today = dateKey(new Date());
  const statuses = [...new Set(items.map(item => item.status))];
  const selectedMeeting = params.hideDetail === "1" ? undefined : items.find(item => item.id === params.meetingId) ?? items[0];
  const locationOptions = [...new Map(items.flatMap(item => [item.room, item.onlineResource].filter(Boolean).map(room => [room!.id, room!] as const))).values()];

  return <PageFrame allowPublic>
    <PageTitle title="Kalender Rapat" description="Lihat jadwal rapat berdasarkan bidang, lokasi, dan metode rapat." action={<div className="calendar-page-actions"><a className="secondary-button" href="/kalender-rapat"><CalendarDays size={15}/> Hari ini</a>{canRequest&&<a className="primary-link" href="/pengajuan-rapat">+ Ajukan Rapat</a>}</div>} />
    <div className="calendar-workspace"><section className="panel calendar-page-panel">
      <div className="calendar-page-toolbar">
        <div className="calendar-page-month">
          <a className="icon-button" href={`/kalender-rapat?month=${changeMonth(-1)}`} aria-label="Bulan sebelumnya"><ChevronLeft /></a>
          <h2>{monthLabel}</h2>
          <a className="icon-button" href={`/kalender-rapat?month=${changeMonth(1)}`} aria-label="Bulan berikutnya"><ChevronRight /></a>
          <a className="secondary-button" href="/kalender-rapat">Hari ini</a>
        </div>
        <div className="calendar-page-legend department-calendar-legend" aria-label="Legenda warna bidang">{departments.map(department=><span key={department.id} title={department.name}><i style={{backgroundColor:department.color}}/>{department.shortName}</span>)}</div>
      </div>
      <form className="filter-row calendar-filter">
        <input type="hidden" name="month" value={selected} />
        <label>Pencarian<input name="q" defaultValue={q} placeholder="Cari judul, bidang, atau ruangan" /></label>
        <label>Bidang<select name="departmentId" defaultValue={departmentId ?? ""}><option value="">Semua bidang</option>{departments.map(department => <option value={department.id} key={department.id} title={department.name}>{department.shortName}</option>)}</select></label>
        <label>Metode<select name="method" defaultValue={params.method ?? ""}><option value="">Semua metode</option><option value="OFFLINE">Offline</option><option value="ONLINE">Online</option><option value="HYBRID">Hybrid</option></select></label>
        <label>Ruangan<select name="roomId" defaultValue={params.roomId ?? ""}><option value="">Semua ruangan</option>{locationOptions.map(room => <option value={room.id} key={room.id}>{room.name}</option>)}</select></label>
        <button className="secondary-button">Terapkan Filter</button>
        {(q || departmentId || method || roomId) && <a className="clear-filter" href={`/kalender-rapat?month=${selected}`}>Hapus filter</a>}
      </form>
      <div className="calendar-view-switch"><span>Tampilan kalender</span><div>{[["month", "Bulan"], ["week", "Minggu"], ["day", "Hari"], ["agenda", "Agenda"]].map(([key, label]) => <a className={view === key ? "active" : ""} href={`/kalender-rapat?month=${selected}&view=${key}`} key={key}>{label}</a>)}</div></div>
      {view !== "agenda" && <div className={`calendar-full-grid ${view}-view`}>
        <div className="calendar-full-weekdays">{["Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu", "Minggu"].map(day => <b key={day}>{day}</b>)}</div>
        <div className="calendar-full-days">{displayCells.map((date, index) => {
          const key = dateKey(date);
          const dayItems = items.filter(item => dateKey(item.startAt) === key);
          const inMonth = date.getUTCMonth() === month - 1;
          return <div className={`calendar-full-day ${inMonth ? "" : "outside-month"}`} key={`${key}-${index}`}>
            <span className={`calendar-day-number ${key === today ? "is-today" : ""}`}>{new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", day: "numeric" }).format(date)}</span>
            <div className="calendar-day-events">{dayItems.slice(0, 3).map(meeting => {
              return <a className={`calendar-full-event department-color ${selectedMeeting?.id === meeting.id ? "is-selected" : ""}`} style={{"--department-color":meeting.department.color} as CSSProperties} href={`/kalender-rapat?month=${selected}&meetingId=${meeting.id}`} key={meeting.id} title={`${timeLabel(meeting.startAt)} ${meeting.title}`}><time>{timeLabel(meeting.startAt)}</time><strong>{meeting.title}</strong><small>{meeting.room?.name ?? meeting.onlineResource?.name ?? "Rapat daring"}</small></a>;
            })}{dayItems.length > 3 && <span className="more-events">+{dayItems.length - 3} agenda lagi</span>}</div>
          </div>;
        })}</div>
      </div>}
      <div className="calendar-page-footer"><span><CalendarDays size={15}/> {items.length} agenda pada {monthLabel}</span><span><Clock3 size={14}/> Waktu ditampilkan dalam WIB</span></div>
    </section><aside className="panel calendar-detail-panel"><div className="panel-head"><h2>Detail Rapat</h2><a href={`/kalender-rapat?month=${selected}&hideDetail=1`} aria-label="Tutup detail">×</a></div>{selectedMeeting ? <><span className="calendar-detail-status" style={{backgroundColor:`color-mix(in srgb, ${selectedMeeting.department.color} 13%, white)`,color:selectedMeeting.department.color}} title={selectedMeeting.department.name}>{selectedMeeting.department.shortName}</span><h3>{selectedMeeting.title}</h3>{auth?<><ul className="calendar-detail-facts"><li><CalendarDays/>{new Intl.DateTimeFormat("id-ID", { dateStyle: "full", timeZone: "Asia/Jakarta" }).format(selectedMeeting.startAt)}</li><li><Clock3/>{timeLabel(selectedMeeting.startAt)}–{timeLabel(selectedMeeting.endAt)} WIB</li><li><MapPin/>{selectedMeeting.room?.name ?? selectedMeeting.onlineResource?.name ?? selectedMeeting.externalLocation ?? "Rapat daring"}</li><li><UsersRound/>{selectedMeeting.expectedParticipants} peserta</li>{selectedMeeting.onlineResource&&<li><span className="calendar-detail-method">{selectedMeeting.breakoutRoomCount} breakout room terpakai dari {selectedMeeting.onlineResource.breakoutRooms}: {selectedMeeting.breakoutRooms.map(room=>room.name).join(", ")||"tidak digunakan"}</span></li>}<li><span className="calendar-detail-method">{selectedMeeting.method}</span></li></ul><hr/><h4>Agenda Rapat</h4><p>{selectedMeeting.description || "Tidak ada deskripsi agenda."}</p><hr/><h4>Kebutuhan Rapat</h4><ul className="calendar-detail-list">{selectedMeeting.requirements.length ? selectedMeeting.requirements.map(req => <li key={req.id}><span>✓</span>{req.type.replaceAll("_", " ")}</li>) : <li>Tidak ada kebutuhan khusus</li>}</ul><hr/><h4>Konsumsi</h4><ul className="calendar-detail-list">{selectedMeeting.consumptions.length ? selectedMeeting.consumptions.map(item => <li key={item.id}>{item.itemType.replaceAll("_", " ")} <b>{item.quantity} {item.unit}</b></li>) : <li>—</li>}</ul><hr/><h4>Evidence Rapat</h4><ul className="calendar-detail-list">{(["DOCUMENTATION", "MINUTES"] as const).map(type => { const doc = selectedMeeting.documents.find(item => item.documentType === type); return <li key={type}>{type === "MINUTES" ? "Notulensi" : "Dokumentasi"}<span>{doc ? "Diunggah" : "Belum diunggah"}</span></li>; })}</ul><a className="primary-link calendar-detail-link" href={`/rapat/${selectedMeeting.id}`}>Lihat Detail Lengkap</a></>:<ul className="calendar-detail-facts"><li><CalendarDays/>{new Intl.DateTimeFormat("id-ID", { dateStyle: "full", timeZone: "Asia/Jakarta" }).format(selectedMeeting.startAt)}</li><li><Clock3/>{timeLabel(selectedMeeting.startAt)}–{timeLabel(selectedMeeting.endAt)} WIB</li><li><MapPin/>{selectedMeeting.room?.name ?? selectedMeeting.onlineResource?.name ?? selectedMeeting.externalLocation ?? "Rapat daring"}</li><li><span className="calendar-detail-method">{selectedMeeting.method}</span></li>{selectedMeeting.onlineResource&&selectedMeeting.breakoutRooms.length>0&&<li><span className="calendar-detail-method">Breakout Zoom: {selectedMeeting.breakoutRooms.map(room=>room.name).join(", ")}</span></li>}</ul>}</> : <p className="empty-state">Pilih agenda untuk melihat detail rapat.</p>}</aside></div>
    {items.length > 0 && <section className="panel calendar-agenda-list"><div className="panel-head"><h2>Agenda Bulan Ini</h2><span>{statuses.length} status rapat</span></div>{items.slice(0, 8).map(meeting => <a className="meeting-row department-color" style={{"--department-color":meeting.department.color} as CSSProperties} href={auth?`/rapat/${meeting.id}`:`/kalender-rapat?month=${selected}&meetingId=${meeting.id}`} key={meeting.id}><div className="meeting-date"><b>{new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", day: "2-digit" }).format(meeting.startAt)}</b><small>{new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", month: "short" }).format(meeting.startAt)}</small></div><div className="meeting-main"><strong>{meeting.title}</strong><small title={meeting.department.name}>{meeting.department.shortName}{auth?` · ${meeting.organizer.name}`:""}</small><small><MapPin/> {meeting.room?.name ?? meeting.onlineResource?.name ?? "Rapat daring"}{auth&&<> <UsersRound/> {meeting.expectedParticipants} peserta</>}</small></div><div className="meeting-when">{timeLabel(meeting.startAt)} – {timeLabel(meeting.endAt)}<span className={`status ${meeting.status.toLowerCase()}`}>{meeting.status}</span></div></a>)}</section>}
  </PageFrame>;
}
