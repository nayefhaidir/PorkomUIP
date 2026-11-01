import { notFound, redirect } from "next/navigation";
import { CalendarDays, MapPin, UsersRound, Paperclip, Clock3, FileCheck2, ClipboardCheck, FileText } from "lucide-react";
import { currentUser, hasPageAccess } from "@/lib/auth";
import { PageFrame, PageTitle } from "@/app/components/page-frame";
import { MeetingActions, DocumentUpload } from "./meeting-actions";
import { GoApiError, goApiFetch } from "@/lib/go-api";

export const dynamic = "force-dynamic";
type DocumentType = "SUPPORTING" | "DOCUMENTATION" | "MINUTES" | "ATTENDANCE_LIST";
type MeetingDetailData = {
  id: string;
  title: string;
  description: string | null;
  startAt: Date;
  endAt: Date;
  status: string;
  method: string;
  isConfidential: boolean;
  allowParticipantOverlap: boolean;
  organizerId: string;
  expectedParticipants: number;
  externalLocation: string | null;
  rejectionReason: string | null;
  breakoutRoomCount: number;
  department: { id: string; name: string; shortName: string; color: string };
  room: { id: string; name: string; capacity: number } | null;
  onlineResource: { id: string; name: string; onlinePlatform: string; breakoutRooms: number } | null;
  breakoutRooms: { id: string; name: string; capacity: number }[];
  zoomBreakoutBookings: { breakoutRoom: { name: string } }[];
  organizer: { id: string; name: string; email?: string };
  attendances: { id: string; user: { id: string; name: string; email?: string } }[];
  guests: { id: string; name: string; email?: string | null; organization: string | null }[];
  documents: { id: string; documentType: DocumentType; filename: string; verifiedAt?: string | null }[];
};
const documentLabels: Record<DocumentType, string> = { SUPPORTING: "Dokumen pendukung", DOCUMENTATION: "Dokumentasi rapat", MINUTES: "Notulensi", ATTENDANCE_LIST: "Daftar hadir" };
const documentIcons: Record<string, typeof Paperclip> = { SUPPORTING: Paperclip, DOCUMENTATION: FileCheck2, MINUTES: FileText, ATTENDANCE_LIST: ClipboardCheck };

export default async function MeetingDetail({ params }: { params: Promise<{ id: string }> }) {
  const auth = await currentUser();
  if (!auth) redirect("/login");
  const { id } = await params;
  let meeting: MeetingDetailData;
  try {
    const result = await goApiFetch<Omit<MeetingDetailData, "startAt" | "endAt"> & { startAt: string; endAt: string }>(`meetings/${encodeURIComponent(id)}`);
    meeting = { ...result, startAt: new Date(result.startAt), endAt: new Date(result.endAt) };
  } catch (error) {
    if (error instanceof GoApiError && error.status === 404) notFound();
    throw error;
  }
  const manager = hasPageAccess(auth.user,"approval",auth.user.role === "ADMIN" || auth.user.role === "APPROVER" && (auth.user.department?.shortName ?? "").toUpperCase().includes("CSR"));
  const allowed = manager || (auth.user.role === "ORGANIZER" && meeting.organizerId === auth.user.id);
  const afterMeeting = meeting.endAt <= new Date();
  const started = meeting.startAt <= new Date();
  const requiredAfter: DocumentType[] = ["DOCUMENTATION", "ATTENDANCE_LIST"];
  const completedAfter = requiredAfter.every(type => meeting.documents.some(document => document.documentType === type && Boolean(document.verifiedAt)));
  const workflowSteps = [
    ["Pengajuan", "Data rapat dikirim oleh penyelenggara."],
    ["Approval", "Permohonan diperiksa oleh pejabat berwenang."],
    ["Reservasi", "Ruang dan resource yang dipilih ditahan."],
    ["Undangan", "Undangan dikirim setelah permohonan disetujui."],
    ["Pelaksanaan", "Rapat dilaksanakan sesuai jadwal."],
    ["Evidence", "Dokumentasi dan daftar hadir diverifikasi."],
    ["Penutupan Reservasi", "Reservasi ditutup setelah evidence wajib terverifikasi."],
  ];
  const workflowIndex = meeting.status === "COMPLETED" ? 6 : meeting.status === "APPROVED" ? afterMeeting ? 5 : started ? 4 : 3 : meeting.status === "SUBMITTED" ? 1 : 0;
  const workflowStopped = ["REJECTED", "CANCELLED"].includes(meeting.status);
  return <PageFrame>
    <PageTitle title={meeting.title} description="Detail rapat, peserta, persetujuan, dan bukti pelaksanaan." action={<a className="secondary-button" href="/kalender-rapat">Kembali ke kalender</a>} />
    <section className={`panel workflow-timeline${workflowStopped ? " workflow-stopped" : ""}`} aria-label="Siklus pengelolaan ruangan">
      <div className="panel-head"><div><h2>Siklus Pengelolaan Ruangan</h2><p>Pengajuan → Approval → Reservasi → Undangan → Pelaksanaan → Evidence → Penutupan Reservasi</p></div><span className={`status ${meeting.status.toLowerCase()}`}>{meeting.status}</span></div>
      <ol>{workflowSteps.map(([label, description], index) => <li key={label} className={index < workflowIndex || meeting.status === "COMPLETED" ? "complete" : index === workflowIndex ? "current" : ""}><span className="workflow-step-number">{index < workflowIndex || meeting.status === "COMPLETED" ? "✓" : index + 1}</span><div><strong>{label}</strong><small>{description}</small></div></li>)}</ol>
      {workflowStopped && <p className="workflow-stop-note">Alur dihentikan dengan status {meeting.status === "REJECTED" ? "ditolak" : "dibatalkan"}; reservasi tidak aktif.</p>}
    </section>
    <div className="detail-grid">
      <section className="panel detail-panel">
        <div className="detail-heading"><span className={`status ${meeting.status.toLowerCase()}`}>{meeting.status}</span>{meeting.isConfidential&&<span className="confidential-badge">Rahasia</span>}{allowed && ["DRAFT", "SUBMITTED"].includes(meeting.status) && <a className="secondary-button" href={`/rapat/${meeting.id}/edit`}>Edit</a>}</div>
        <div className="detail-facts">
          <p><CalendarDays/>{new Intl.DateTimeFormat("id-ID", { dateStyle: "full", timeZone: "Asia/Jakarta" }).format(meeting.startAt)}</p>
          <p><Clock3/>{new Intl.DateTimeFormat("id-ID", { timeStyle: "short", timeZone: "Asia/Jakarta" }).format(meeting.startAt)} – {new Intl.DateTimeFormat("id-ID", { timeStyle: "short", timeZone: "Asia/Jakarta" }).format(meeting.endAt)} WIB</p>
          <p><MapPin/>{[meeting.room?.name, meeting.onlineResource?.name, meeting.externalLocation].filter(Boolean).join(" + ") || "Ruang belum dipilih"} · {meeting.department.name}</p>
          {meeting.onlineResource&&<p><strong>{meeting.onlineResource.onlinePlatform === "TEAMS" ? "Microsoft Teams" : "Zoom"}:</strong> {meeting.onlineResource.onlinePlatform === "TEAMS" ? "Rapat online; breakout room tidak tersedia pada portal ini." : meeting.breakoutRoomCount ? `${(meeting.zoomBreakoutBookings.map(booking=>booking.breakoutRoom.name).length?meeting.zoomBreakoutBookings.map(booking=>booking.breakoutRoom.name):meeting.breakoutRooms.map(room=>room.name)).join(", ")} terpakai · kapasitas ${meeting.onlineResource.breakoutRooms} room` : "Tanpa breakout room."}</p>}
          <p><UsersRound/>{meeting.expectedParticipants} peserta · {meeting.attendances.length} undangan akun · {meeting.guests.length} tamu manual</p>
          {meeting.allowParticipantOverlap&&<p className="participant-overlap-note"><UsersRound/> Peserta boleh tercantum pada rapat lain dengan jadwal bersamaan; konflik ruangan dan penyelenggara tetap dicegah.</p>}
          <p><strong>Penyelenggara:</strong> {meeting.organizer.name}</p>
        </div>
        <h2>Agenda</h2><p className="body-copy">{meeting.description || "Tidak ada deskripsi agenda."}</p>
        <h2><UsersRound/> Peserta</h2>
        <ul className="people-list">{meeting.attendances.map(attendance => <li key={attendance.id}>{attendance.user.name}<small>{attendance.user.email} · peserta internal</small></li>)}{meeting.guests.map(guest => <li key={guest.id}>{guest.name}<small>{[guest.email, guest.organization, "tamu manual"].filter(Boolean).join(" · ")}</small></li>)}</ul>
      </section>
      <aside className="detail-side">
        <section className="panel"><h2>Alur Persetujuan</h2><p>Status saat ini: <strong>{meeting.status}</strong></p>{meeting.rejectionReason && <p className="form-error">{meeting.rejectionReason}</p>}<MeetingActions id={meeting.id} status={meeting.status} canReview={manager} canCancel={manager||(auth.user.role==="ORGANIZER"&&meeting.organizerId===auth.user.id)} canDelete={auth.user.role==="ADMIN"}/></section>
        <section className="panel evidence-panel"><div className="evidence-panel-title"><span><Paperclip size={18}/></span><div><h2>Dokumen &amp; Evidence</h2><p>{afterMeeting?"Kelengkapan bukti setelah rapat.":"Dokumen pendukung untuk proses persetujuan."}</p></div></div>
          <ul className="evidence-document-list">{(Object.entries(documentLabels) as [DocumentType, string][]).map(([type, label]) => { const doc = meeting.documents.find(item => item.documentType === type); const required = afterMeeting && requiredAfter.includes(type); const verified = Boolean(doc?.verifiedAt); const Icon=documentIcons[type]; return <li key={type} className={required&&(!doc||!verified)?"missing":""}><span className={`evidence-document-icon ${type.toLowerCase()}`}><Icon size={17}/></span><span className="evidence-document-copy"><strong>{label}</strong><small>{doc?`${doc.filename}${required&&!verified?" · Menunggu verifikasi":""}`:required?"Wajib diunggah":"Opsional"}</small></span>{doc?<a className="evidence-download" href={`/api/documents/${doc.id}`}><FileText size={14}/> Buka</a>:<span className={required?"evidence-required":"evidence-optional"}>{required?"Wajib":"Belum ada"}</span>}</li>; })}</ul>
          {afterMeeting && !completedAfter && <p className="priority-notice">PENTING · Lengkapi dokumentasi rapat dan daftar hadir. Pengingat akan dikirim kepada pembuat rapat.</p>}
          {afterMeeting && completedAfter && meeting.status === "COMPLETED" && <p className="form-success">Evidence lengkap dan telah diverifikasi.</p>}
          <DocumentUpload id={meeting.id} allowed={allowed} afterMeeting={afterMeeting}/>
        </section>
      </aside>
    </div>
  </PageFrame>;
}
