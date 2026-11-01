import { CalendarDays, CalendarCheck, Clock3, UsersRound, Building2, Camera, FileText, ChevronLeft, ChevronRight, MapPin, ArrowRight } from "lucide-react";
import { currentUser } from "@/lib/auth";
import { goApiFetch, goApiItems } from "@/lib/go-api";
import { AppShell } from "@/app/components/app-shell";
import AnnouncementSequence from "./beranda/announcement-sequence";
import AnnouncementManager from "./beranda/announcement-manager";
import type { CSSProperties } from "react";

export const dynamic = "force-dynamic";
type MeetingStatus = "DRAFT" | "SUBMITTED" | "APPROVED" | "REJECTED" | "CANCELLED" | "COMPLETED";
type DashboardMeeting = {
  id: string;
  title: string;
  startAt: Date;
  endAt: Date;
  status: MeetingStatus;
  expectedParticipants: number;
  notes: string | null;
  externalLocation: string | null;
  department: { id: string; name: string; shortName: string; color: string };
  room: { id: string; name: string } | null;
  onlineResource: { id: string; name: string } | null;
  documents: { id: string; documentType?: string }[];
};
type Announcement = { id: string; type: string; title: string; body: string; publishedAt: string; expiresAt: string | null; imageUrl: string | null };
const jakarta = (date: Date, options: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", ...options }).format(date);
const dateKey = (date: Date) => jakarta(date, { year: "numeric", month: "2-digit", day: "2-digit" });
const timeLabel = (date: Date) => jakarta(date, { hour: "2-digit", minute: "2-digit", hour12: false });
function CalendarPanel({ year, month, items,isPublic }: { year: number; month: number; items: DashboardMeeting[];isPublic:boolean }) {
  const first = new Date(year, month, 1), offset = (first.getDay() + 6) % 7, count = new Date(year, month + 1, 0).getDate();
  const cells = Array.from({ length: Math.ceil((offset + count) / 7) * 7 }, (_, i) => new Date(Date.UTC(year, month, i - offset + 1, 12) - 7 * 3600000));
  return <section className="panel calendar-panel"><div className="panel-head"><h2><CalendarDays/> Kalender Rapat</h2><div className="calendar-tools"><a className="icon-button" href={`/kalender-rapat?month=${new Date(year, month - 1, 1).toISOString().slice(0, 7)}`} aria-label="Bulan sebelumnya"><ChevronLeft/></a><b className="month">{new Intl.DateTimeFormat("id-ID", { month: "long", year: "numeric", timeZone: "UTC" }).format(new Date(Date.UTC(year, month, 1)))}</b><a className="icon-button" href={`/kalender-rapat?month=${new Date(year, month + 1, 1).toISOString().slice(0, 7)}`} aria-label="Bulan berikutnya"><ChevronRight/></a><a className="tab active" href="/kalender-rapat">Bulan</a><a className="tab" href="/kalender-rapat?view=week">Minggu</a><a className="tab" href="/kalender-rapat?view=agenda">Agenda</a></div></div><div className="legend">{[...new Map(items.map(item => [item.department.id,item.department])).values()].map(department => <span key={department.id} title={department.name}><i className="dot" style={{backgroundColor:department.color}}/>{department.shortName}</span>)}</div><div className="calendar-grid"><div className="weekdays">{["Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu", "Minggu"].map(day => <b key={day}>{day}</b>)}</div><div className="days">{cells.map((date, i) => { const inMonth = date.getMonth() === month, agenda = items.filter(item => dateKey(item.startAt) === dateKey(date)); return <div className={`day ${inMonth ? "" : "muted"}`} key={i}><span className={dateKey(date) === dateKey(new Date()) ? "today" : ""}>{date.getDate()}</span>{agenda.map(item => <a href={isPublic ? `/kalender-rapat?month=${date.getUTCFullYear()}-${String(date.getUTCMonth()+1).padStart(2,"0")}` : `/rapat/${item.id}`} className="event department-color" style={{"--department-color":item.department.color} as CSSProperties} key={item.id} title={`${timeLabel(item.startAt)} · ${item.title}`}><small>{timeLabel(item.startAt)}</small> {item.title}</a>)}</div>; })}</div></div></section>;
}

export default async function HomePage() {
  const auth = await currentUser();
  const now = new Date(), year = Number(jakarta(now, { year: "numeric" })), month = Number(jakarta(now, { month: "2-digit" })) - 1;
  const from = new Date(Date.UTC(year, month, 1) - 7 * 3600000), to = new Date(Date.UTC(year, month + 1, 1) - 7 * 3600000), todayKey = dateKey(now);
  const monthRange = new URLSearchParams({ from: from.toISOString(), to: new Date(to.getTime() - 1).toISOString() });
  const availabilityRange = new URLSearchParams({ from: now.toISOString(), to: new Date(now.getTime() + 1000).toISOString() });
  const publicSchedule = auth ? null : goApiFetch<{ meetings: Record<string, unknown>[]; announcements: Announcement[] }>(`public/schedule?${monthRange}`);
  const announcementsPromise = auth
    ? Promise.all(["MANAGEMENT_TALK", "UIP_NEWS", "GENERAL"].map(type => goApiFetch<Announcement[]>(`announcements?type=${type}`))).then(groups => groups.flat())
    : publicSchedule!.then(result => result.announcements);
  const [meetingRows, roomResponse, announcements] = await Promise.all([
    auth
      ? goApiItems<Record<string, unknown>>("meetings", monthRange)
      : publicSchedule!.then(result => result.meetings),
    goApiFetch<{ rooms: { available: boolean }[] }>(`availability?${availabilityRange}`),
    announcementsPromise,
  ]);
  const meetings: DashboardMeeting[] = meetingRows.map(row => ({
    ...row,
    startAt: new Date(String(row.startAt)),
    endAt: new Date(String(row.endAt)),
  } as unknown as DashboardMeeting)).filter(item => auth
    ? ["SUBMITTED", "APPROVED", "COMPLETED"].includes(item.status)
    : item.status === "APPROVED");
  const rooms = roomResponse.rooms;
  const managementTalk = announcements.filter(item => item.type === "MANAGEMENT_TALK").slice(0, 5);
  const uipNews = announcements.filter(item => item.type === "UIP_NEWS").slice(0, 5);
  const generalAnnouncements = announcements.filter(item => item.type === "GENERAL").slice(0, 5);
  const availableCount = rooms.filter(room => room.available).length;
  const today = meetings.filter(item => dateKey(item.startAt) === todayKey), upcoming = meetings.filter(item => item.startAt >= now).slice(0, 3);
  const totalParticipants = meetings.reduce((sum, item) => sum + item.expectedParticipants, 0);
  const duration = meetings.reduce((sum, item) => sum + Math.max(0, Math.round((item.endAt.getTime() - item.startAt.getTime()) / 60000)), 0);
  const stats = [
    { title: "Total Rapat Bulan Ini", value: String(meetings.length), icon: CalendarCheck, color: "blue" },
    { title: "Total Peserta", value: String(totalParticipants), icon: UsersRound, color: "green" },
    { title: "Total Durasi Rapat", value: Math.floor(duration / 60) + " jam " + duration % 60 + " menit", icon: Clock3, color: "orange" },
    { title: "Ruang Tersedia", value: String(availableCount), suffix: "dari " + rooms.length + " ruangan", icon: Building2, color: "purple" },
    { title: "Rapat dengan Dokumentasi", value: String(meetings.filter(item => item.documents.length).length), suffix: "dari " + meetings.length + " rapat", icon: Camera, color: "blue" },
    { title: "Rapat dengan Notulensi", value: String(meetings.filter(item => item.notes?.trim()).length), suffix: "dari " + meetings.length + " rapat", icon: FileText, color: "blue" },
  ];
  const weekday = new Intl.DateTimeFormat("id-ID", { weekday: "long", timeZone: "Asia/Jakarta" }).format(now), formattedDate = jakarta(now, { day: "numeric", month: "long", year: "numeric" });
  const canManageAnnouncements = Boolean(auth&&(auth.user.role === "ADMIN" || auth.user.role === "APPROVER"));
  const canManageTalk = Boolean(auth&&(auth.user.role === "ADMIN" || auth.user.role === "APPROVER" && /\b(GM|SRM)\b/i.test(auth.user.jobTitle ?? "")));
  const canManageNews = Boolean(auth&&(auth.user.role === "ADMIN" || auth.user.role === "APPROVER" && (auth.user.department?.shortName ?? "").toUpperCase().includes("CSR")));
  const meetingHref=(id:string)=>auth?`/rapat/${id}`:`/kalender-rapat?month=${year}-${String(month+1).padStart(2,"0")}&meetingId=${id}`;
  const dashboard = <>
    <div className="hero"><div><h1>Selamat Datang di<br/>Portal Komunikasi Unit Induk Proyek - Porkom UIP</h1><p>Satu portal untuk koordinasi, penjadwalan, dan monitoring<br/>seluruh kegiatan rapat Unit Induk Proyek.</p></div><blockquote>“Terhubung dalam informasi,<br/>Bergerak dalam kolaborasi.”</blockquote></div>
    <div className="content"><div className="stats">{stats.map(({ title, value, suffix, icon: Icon, color }) => <article className="stat" key={title}><span className={`stat-icon ${color}`}><Icon/></span><div><small>{title}</small><strong>{value}</strong>{suffix && <span className="stat-suffix">{suffix}</span>}</div></article>)}</div>
      <div className="dashboard-grid"><div className="dashboard-main-column"><CalendarPanel year={year} month={month} items={meetings} isPublic={!auth}/>
        <section className="panel upcoming"><div className="panel-head"><h2><CalendarDays/> Agenda Mendatang</h2><a className="all-link" href="/kalender-rapat">Lihat Semua <ArrowRight size={14}/></a></div><div className="upcoming-cards">{upcoming.length ? upcoming.map(item => <a className="upcoming-card" href={meetingHref(item.id)} key={item.id}><b className="datebox">{jakarta(item.startAt, { day: "2-digit" })}<small>{jakarta(item.startAt, { month: "short" })}</small></b><div><strong>{item.title}</strong><small>◷ {timeLabel(item.startAt)} – {timeLabel(item.endAt)}</small><small><UsersRound/> <span title={item.department.name}>{item.department.shortName}</span></small><small><MapPin/> {item.room?.name ?? item.onlineResource?.name ?? item.externalLocation ?? "Rapat daring"}</small></div></a>) : <p className="empty-state">Belum ada rapat terjadwal berikutnya.</p>}</div></section>
      </div><div className="dashboard-side-column"><section className="panel agenda"><div className="panel-head"><h2><CalendarDays/> Agenda Hari Ini</h2><small>{weekday}, {formattedDate}</small></div>{today.length ? today.map(item => <a href={meetingHref(item.id)} className="agenda-item department-color" style={{"--department-color":item.department.color} as CSSProperties} key={item.id}><time>{timeLabel(item.startAt)} – {timeLabel(item.endAt)}</time><div><strong>{item.title}</strong><small><UsersRound/> <span title={item.department.name}>{item.department.shortName}</span> <MapPin/> {item.room?.name ?? item.onlineResource?.name ?? item.externalLocation ?? "Rapat daring"}</small></div><span className={`status ${item.status.toLowerCase()}`}>{item.status}</span></a>) : <p className="empty-state">Belum ada agenda rapat hari ini.</p>}<a className="all-link" href="/kalender-rapat">Lihat Semua Agenda Hari Ini <ArrowRight size={14}/></a></section>
        <AnnouncementSequence managementTalk={managementTalk} news={uipNews} canManageTalk={canManageTalk} canManageNews={canManageNews} publicMode={!auth}/>
        <AnnouncementManager initial={generalAnnouncements} canManage={canManageAnnouncements}/>
      </div>
      </div>
    </div>
  </>;
  if(!auth)return <AppShell publicMode user={{name:"Pengunjung",role:"PUBLIC",department:null,pageAccess:["beranda","kalender"]}}>{dashboard}</AppShell>;
  return <AppShell user={{ name: auth.user.name, role: auth.user.role, department: auth.user.department?.name ?? null, pageAccess:auth.user.pageAccess }}>{dashboard}</AppShell>;
}
