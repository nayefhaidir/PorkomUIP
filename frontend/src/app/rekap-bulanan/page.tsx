import { PageFrame, PageTitle } from "@/app/components/page-frame";
import { Building2, CalendarDays, Camera, Clock3, FileText, UsersRound } from "lucide-react";
import ReportControls from "./report-controls";
import { currentUser } from "@/lib/auth";
import { redirect } from "next/navigation";
import { goApiFetch } from "@/lib/go-api";

export const dynamic = "force-dynamic";
type MeetingMethod = "OFFLINE" | "ONLINE" | "HYBRID";
type MeetingStatus = "DRAFT" | "SUBMITTED" | "APPROVED" | "REJECTED" | "CANCELLED" | "COMPLETED";
const meetingMethods: MeetingMethod[] = ["OFFLINE", "ONLINE", "HYBRID"];
type ReportMeeting = {
  id: string;
  title: string;
  startAt: Date;
  endAt: Date;
  status: MeetingStatus;
  method: MeetingMethod;
  expectedParticipants: number;
  guestCategory: string;
  notes: string | null;
  department: { id: string; name: string };
  room: { name: string } | null;
  onlineResource: { name: string } | null;
  consumptions: { id: string; itemType: string; quantity: number; unit: string }[];
  documents: { documentType: string }[];
};
type Department = { id: string; name: string };
const fmtDate = (value: Date, options: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", ...options }).format(value);
const fmtTime = (value: Date) => fmtDate(value, { hour: "2-digit", minute: "2-digit", hour12: false });
const monthTitle = (month: string) => fmtDate(new Date(month + "-01T12:00:00+07:00"), { month: "long", year: "numeric" });
const methodLabels: Record<MeetingMethod, string> = { OFFLINE: "Offline (Kantor)", ONLINE: "Online (Zoom)", HYBRID: "Hybrid" };
const methodColors: Record<MeetingMethod, string> = { OFFLINE: "#1476e8", ONLINE: "#39b982", HYBRID: "#8952e8" };
const consumptionLabels: Record<string, string> = { SNACK_BOX: "Snack box", RICE_BOX: "Rice box", COFFEE_BREAK: "Coffee break", LUNCH: "Makan siang", OTHER: "Lainnya" };
const statusLabels: Record<MeetingStatus, string> = { DRAFT: "Draft", SUBMITTED: "Menunggu", APPROVED: "Disetujui", REJECTED: "Ditolak", CANCELLED: "Dibatalkan", COMPLETED: "Selesai" };

type Slice = { label: string; value: number; color: string };
function Donut({ data, total, centerLabel }: { data: Slice[]; total: number; centerLabel: string }) {
  let cursor = 0;
  const stops = data.filter(item => item.value > 0).map(item => { const start = cursor; cursor += item.value / Math.max(1, total) * 100; return item.color + " " + start + "% " + cursor + "%"; });
  return <div className="report-donut" role="img" aria-label={data.map(item => item.label + ": " + item.value).join(", ")} style={{ background: stops.length ? "conic-gradient(" + stops.join(", ") + ")" : "#e8eef4" }}><div><strong>{total}</strong><small>{centerLabel}</small></div></div>;
}

export default async function ReportPage({ searchParams }: { searchParams: Promise<Record<string, string | undefined>> }) {
  const auth = await currentUser();
  if (!auth) redirect("/login");
  if (auth.user.role === "PARTICIPANT") redirect("/beranda");
  const p = await searchParams;
  const jakartaNow = new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Jakarta", year: "numeric", month: "2-digit" }).format(new Date());
  const month = p.month && /^\d{4}-(0[1-9]|1[0-2])$/.test(p.month) ? p.month : jakartaNow;
  const year = Number(month.slice(0, 4));
  const departmentId = p.departmentId || undefined;
  const method = p.method && ["OFFLINE", "ONLINE", "HYBRID"].includes(p.method) ? p.method as MeetingMethod : undefined;
  const q = p.q?.trim().slice(0, 180);
  const reportQuery = new URLSearchParams({ month, ...(departmentId ? { departmentId } : {}), ...(method ? { method } : {}), ...(q ? { q } : {}) });
  const [report, departments] = await Promise.all([
    goApiFetch<{
      items: Record<string, unknown>[];
      yearMeetings: { month: number; meetings: number; expectedParticipants: number }[];
      previousMeetings: number;
      previousParticipants: number;
      activeRooms: number;
    }>(`reports/monthly?${reportQuery}`),
    goApiFetch<Department[]>("departments"),
  ]);
  const meetings: ReportMeeting[] = report.items.map(item => ({
    ...item,
    startAt: new Date(String(item.startAt)),
    endAt: new Date(String(item.endAt)),
  } as unknown as ReportMeeting));
  const totalMeetings = meetings.length;
  const totalParticipants = meetings.reduce((sum, meeting) => sum + meeting.expectedParticipants, 0);
  const durationMinutes = meetings.reduce((sum, meeting) => sum + Math.max(0, Math.round((meeting.endAt.getTime() - meeting.startAt.getTime()) / 60000)), 0);
  const methods = meetingMethods.map((key, index) => ({ key, label: methodLabels[key], color: methodColors[key], value: meetings.filter(item => item.method === key).length, index }));
  const locationsByName = new Map<string, number>();
  for (const meeting of meetings) { const name = meeting.room?.name ?? meeting.onlineResource?.name ?? "Lokasi belum ditentukan"; locationsByName.set(name, (locationsByName.get(name) ?? 0) + 1); }
  const locationColors = ["#126ad5", "#3c9bea", "#81c8f0", "#efa932", "#9aaabd", "#8952e8"];
  const locations = [...locationsByName].map(([label, value], index) => ({ label, value, color: locationColors[index % locationColors.length] })).sort((a, b) => b.value - a.value);
  const departmentsByName = new Map<string, number>();
  for (const meeting of meetings) departmentsByName.set(meeting.department.name, (departmentsByName.get(meeting.department.name) ?? 0) + 1);
  const departmentRows = [...departmentsByName].map(([name, value], index) => ({ name, value, color: ["#277fe7", "#31b581", "#f0a934", "#8952e8", "#35a8bc"][index % 5] })).sort((a, b) => b.value - a.value);
  const consumptions = ["SNACK_BOX", "RICE_BOX", "COFFEE_BREAK", "LUNCH", "OTHER"].map((key, index) => ({
    key, label: consumptionLabels[key], unit: key === "SNACK_BOX" || key === "RICE_BOX" ? "box" : "porsi",
    value: meetings.reduce((sum, meeting) => sum + (meeting.consumptions.find(item => item.itemType === key)?.quantity ?? 0), 0),
    color: ["#20ad79", "#2786e8", "#e9a12a", "#8752df", "#32a9bc"][index],
  })).filter(item => item.value > 0);
  const documentation = meetings.filter(meeting => meeting.documents.some(document => document.documentType === "DOCUMENTATION")).length;
  const minutes = meetings.filter(meeting => meeting.documents.some(document => document.documentType === "MINUTES") || Boolean(meeting.notes?.trim())).length;
  const previousMeetings = report.previousMeetings;
  const previousParticipants = report.previousParticipants;
  const monthCounts = Array.from({ length: 12 }, (_, index) => ({ label: new Intl.DateTimeFormat("id-ID", { month: "short", timeZone: "UTC" }).format(new Date(Date.UTC(year, index, 1, 12))), meetings: 0, participants: 0 }));
  for (const item of report.yearMeetings) { const index = item.month - 1; if (monthCounts[index]) { monthCounts[index].meetings = item.meetings; monthCounts[index].participants = item.expectedParticipants; } }
  const maxTrend = Math.max(1, ...monthCounts.map(item => item.meetings), ...monthCounts.map(item => item.participants / 10));
  const meetingPoints = monthCounts.map((item, index) => (32 + index * 48) + "," + (138 - item.meetings / maxTrend * 105)).join(" ");
  const participantPoints = monthCounts.map((item, index) => (32 + index * 48) + "," + (138 - item.participants / 10 / maxTrend * 105)).join(" ");
  const trendArea = "32,138 " + meetingPoints + " 560,138";
  const maxDepartment = Math.max(1, ...departmentRows.map(item => item.value));
  const printMeta = "Periode " + monthTitle(month) + (departmentId ? " · " + (departments.find(item => item.id === departmentId)?.name ?? "") : " · Semua Bidang") + (method ? " · " + methodLabels[method] : " · Semua Metode");
  const query = new URLSearchParams({ month, ...(departmentId ? { departmentId } : {}), ...(method ? { method } : {}), ...(q ? { q } : {}) }).toString();
  const evidencePct = totalMeetings ? Math.round(documentation / totalMeetings * 100) : 0;
  const minutesPct = totalMeetings ? Math.round(minutes / totalMeetings * 100) : 0;
  const meetingChange = previousMeetings ? Math.round((totalMeetings - previousMeetings) / previousMeetings * 100) : totalMeetings ? 100 : 0;
  const participantChange = previousParticipants ? Math.round((totalParticipants - previousParticipants) / previousParticipants * 100) : totalParticipants ? 100 : 0;

  return <PageFrame>
    <PageTitle title="Rekap Bulanan" description="Ringkasan kegiatan rapat Unit Induk Proyek pada periode terpilih." action={<ReportControls query={query}/>} />
    <div className="report-print-heading"><h1>Rekap Bulanan</h1><p>{printMeta}</p><small>Data diperbarui langsung dari sistem · Dicetak pada {fmtDate(new Date(), { dateStyle: "long", timeStyle: "short" })} WIB</small></div>
    <form className="filter-row report-filters"><label>Periode<input type="month" name="month" defaultValue={month}/></label><label>Bidang<select name="departmentId" defaultValue={departmentId ?? ""}><option value="">Semua Bidang</option>{departments.map(item => <option value={item.id} key={item.id}>{item.name}</option>)}</select></label><label>Metode Rapat<select name="method" defaultValue={method ?? ""}><option value="">Semua Metode</option>{Object.entries(methodLabels).map(([key, label]) => <option value={key} key={key}>{label}</option>)}</select></label><label className="report-search">Cari judul rapat<input name="q" defaultValue={q ?? ""} placeholder="Cari judul rapat…"/></label><button className="secondary-button">Terapkan Filter</button></form>
    <div className="report-stats">
      <article className="report-stat"><span className="report-stat-icon blue"><CalendarDays/></span><div><small>Total Rapat</small><strong>{totalMeetings}</strong><em className={meetingChange < 0 ? "down" : ""}>{meetingChange >= 0 ? "↑ " : "↓ "}{Math.abs(meetingChange)}% <small>vs bulan lalu</small></em></div></article>
      <article className="report-stat"><span className="report-stat-icon green"><UsersRound/></span><div><small>Total Peserta</small><strong>{totalParticipants} <small>orang</small></strong><em className={participantChange < 0 ? "down" : ""}>{participantChange >= 0 ? "↑ " : "↓ "}{Math.abs(participantChange)}% <small>vs bulan lalu</small></em></div></article>
      <article className="report-stat"><span className="report-stat-icon orange"><Clock3/></span><div><small>Total Durasi</small><strong>{Math.floor(durationMinutes / 60)} jam {durationMinutes % 60} menit</strong></div></article>
      <article className="report-stat"><span className="report-stat-icon purple"><Building2/></span><div><small>Ruangan Digunakan</small><strong>{locations.length} <small>ruangan</small></strong><em>{report.activeRooms} total aktif</em></div></article>
      <article className="report-stat"><span className="report-stat-icon blue"><Camera/></span><div><small>Rapat dengan Dokumentasi</small><strong>{documentation}</strong><i className="stat-progress"><b style={{ width: evidencePct + "%" }}/></i><em>{evidencePct}% kelengkapan</em></div></article>
      <article className="report-stat"><span className="report-stat-icon blue"><FileText/></span><div><small>Rapat dengan Notulensi</small><strong>{minutes}</strong><i className="stat-progress"><b style={{ width: minutesPct + "%" }}/></i><em>{minutesPct}% kelengkapan</em></div></article>
    </div>
    <div className="report-grid report-charts">
      <section className="panel report-chart"><div className="panel-head"><h2>Jumlah Rapat per Bidang</h2></div>{departmentRows.length ? <div className="department-bars">{departmentRows.map(row => <div className="department-bar" key={row.name}><b>{row.value}</b><i style={{ height: Math.max(8, row.value / maxDepartment * 100) + "%", background: row.color }}/><span>{row.name}</span></div>)}</div> : <p className="empty-state">Belum ada data pada periode ini.</p>}</section>
      <section className="panel report-chart"><div className="panel-head"><h2>Jenis Rapat</h2></div><div className="donut-chart-layout"><Donut data={methods.map(item => ({ label: item.label, value: item.value, color: item.color }))} total={totalMeetings} centerLabel="Rapat"/><ul className="chart-legend">{methods.map(item => <li key={item.key}><i style={{ background: item.color }}/><span>{item.label}<small>{item.value} rapat</small></span><b>{totalMeetings ? Math.round(item.value / totalMeetings * 100) : 0}%</b></li>)}</ul></div></section>
      <section className="panel report-chart"><div className="panel-head"><h2>Lokasi Rapat</h2></div><div className="donut-chart-layout"><Donut data={locations} total={totalMeetings} centerLabel="Rapat"/><ul className="chart-legend">{locations.map(item => <li key={item.label}><i style={{ background: item.color }}/><span>{item.label}<small>{item.value} rapat</small></span><b>{totalMeetings ? Math.round(item.value / totalMeetings * 100) : 0}%</b></li>)}</ul></div></section>
      <section className="panel report-chart"><div className="panel-head"><h2>Konsumsi Rapat (Total)</h2></div><div className="consumption-bars">{consumptions.length ? consumptions.map(item => { const max = Math.max(1, ...consumptions.map(value => value.value)); return <div className="consumption-bar" key={item.key}><span>{item.label}</span><b>{item.value} {item.unit}</b><i><em style={{ width: item.value / max * 100 + "%", background: item.color }}/></i></div>; }) : <p className="empty-state">Belum ada permintaan konsumsi.</p>}</div></section>
    </div>
    <div className="report-grid report-lower-charts">
      <section className="panel report-chart report-trend"><div className="panel-head"><h2>Tren Jumlah Rapat</h2><span className="trend-legend"><i/>Jumlah Rapat <i/>Jumlah Peserta</span></div><svg viewBox="0 0 590 175" role="img" aria-label={"Tren rapat dan peserta sepanjang tahun " + year}><line x1="30" y1="138" x2="560" y2="138"/><line x1="30" y1="85" x2="560" y2="85"/><line x1="30" y1="32" x2="560" y2="32"/><polygon points={trendArea} fill="#2287ed" opacity=".12"/><polyline points={meetingPoints} fill="none" stroke="#1678ed" strokeWidth="3"/><polyline points={participantPoints} fill="none" stroke="#35b781" strokeWidth="3"/>{monthCounts.map((item, index) => <g key={item.label}><circle cx={32 + index * 48} cy={138 - item.meetings / maxTrend * 105} r="3.5" fill="#1678ed"/><circle cx={32 + index * 48} cy={138 - item.participants / 10 / maxTrend * 105} r="3.5" fill="#35b781"/><text x={32 + index * 48} y="159" textAnchor="middle">{item.label}</text></g>)}</svg></section>
      <section className="panel report-chart report-method"><div className="panel-head"><h2>Metode Rapat</h2></div><div className="donut-chart-layout"><Donut data={methods.map(item => ({ label: item.label, value: item.value, color: item.color }))} total={totalMeetings} centerLabel="Rapat"/><ul className="chart-legend">{methods.map(item => <li key={item.key}><i style={{ background: item.color }}/><span>{item.label}<small>{item.value} rapat</small></span><b>{totalMeetings ? Math.round(item.value / totalMeetings * 100) : 0}%</b></li>)}</ul></div></section>
      <section className="panel report-chart report-evidence"><div className="panel-head"><h2>Status Kelengkapan Evidence</h2></div>{[["Dokumentasi Rapat", documentation, evidencePct, "#26b57e"], ["Notulensi Rapat", minutes, minutesPct, "#2786e8"]].map(([label, value, percent, color]) => <div className="evidence-row" key={String(label)}><div><strong>{label}</strong><b>{percent}%</b></div><i><em style={{ width: percent + "%", background: color }}/></i><small>{value} dari {totalMeetings} rapat</small></div>)}</section>
    </div>
    <section className="panel list-panel report-table-panel"><div className="panel-head"><h2>Daftar Rapat Periode {monthTitle(month)}</h2><span>{meetings.length} rapat · {printMeta}</span></div><div className="table-scroll"><table className="report-table"><thead><tr><th>No</th><th>Tanggal</th><th>Waktu</th><th>Judul Rapat</th><th>Bidang</th><th>Metode</th><th>Lokasi / Zoom</th><th>Peserta</th><th>Tamu</th><th>Konsumsi</th><th>Dokumentasi</th><th>Notulensi</th><th>Status</th></tr></thead><tbody>{meetings.map((item, index) => { const doc = item.documents.some(record => record.documentType === "DOCUMENTATION"), minute = item.documents.some(record => record.documentType === "MINUTES") || Boolean(item.notes?.trim()); const food = item.consumptions.filter(value => value.quantity > 0); return <tr key={item.id}><td>{index + 1}</td><td>{fmtDate(item.startAt, { day: "numeric", month: "short", year: "numeric" })}</td><td>{fmtTime(item.startAt)}–{fmtTime(item.endAt)}</td><td><a href={"/rapat/" + item.id}>{item.title}</a></td><td>{item.department.name}</td><td><span className="method-tag">{methodLabels[item.method]}</span></td><td>{item.room?.name ?? item.onlineResource?.name ?? "—"}</td><td>{item.expectedParticipants}</td><td>{item.guestCategory === "VIP" ? "VIP" : "Biasa"}</td><td>{food.length ? food.map(value => <small className="consumption-cell" key={value.id}>{consumptionLabels[value.itemType]}: {value.quantity}</small>) : "—"}</td><td><span className={"evidence-check " + (doc ? "yes" : "")}>{doc ? "✓" : "·"}</span></td><td><span className={"evidence-check " + (minute ? "yes" : "")}>{minute ? "✓" : "·"}</span></td><td><span className={"status " + item.status.toLowerCase()}>{statusLabels[item.status]}</span></td></tr>; })}</tbody></table>{!meetings.length && <p className="empty-state">Tidak ada rapat aktif pada periode dan filter yang dipilih.</p>}</div></section>
  </PageFrame>;
}
