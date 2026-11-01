import { redirect } from "next/navigation";
import { currentUser } from "@/lib/auth";
import { goApiFetch } from "@/lib/go-api";
import { PageFrame, PageTitle } from "@/app/components/page-frame";
import { AlertTriangle, ClipboardList, FileCheck2, FileText, ShieldCheck } from "lucide-react";
import { VerificationAction } from "./verification-action";

export const dynamic = "force-dynamic";
const documentTypes = ["SUPPORTING", "DOCUMENTATION", "MINUTES", "ATTENDANCE_LIST"] as const;
type DocumentType = typeof documentTypes[number];
const labels: Record<DocumentType, string> = {
  SUPPORTING: "Dokumen pendukung",
  DOCUMENTATION: "Dokumentasi",
  MINUTES: "Notulensi",
  ATTENDANCE_LIST: "Daftar hadir",
};
const dateTime = (date: string) => new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", dateStyle: "medium", timeStyle: "short" }).format(new Date(date));

type DocumentItem = {
  id: string;
  filename: string;
  documentType: DocumentType;
  createdAt: string;
  verifiedAt: string | null;
  uploadedBy: { name: string } | null;
  meeting: { id: string; title: string; startAt: string; department: { name: string } };
};
type MissingMeeting = {
  id: string;
  title: string;
  endAt: string;
  department: { name: string };
  organizer: { name: string };
  documents: { documentType: DocumentType; verified: boolean }[];
};

export default async function ManagementPage({ searchParams }: { searchParams: Promise<Record<string, string | undefined>> }) {
  const auth = await currentUser();
  if (!auth) redirect("/login");
  if (auth.user.role !== "ADMIN") redirect("/beranda");

  const params = await searchParams;
  const page = Math.max(1, Number(params.page) || 1);
  const q = params.q?.trim().slice(0, 120) ?? "";
  const type = documentTypes.includes(params.type as DocumentType) ? params.type as DocumentType : undefined;
  const query = new URLSearchParams({ page: String(page), pageSize: "50", ...(q ? { q } : {}), ...(type ? { type } : {}) });
  const data = await goApiFetch<{
    documents: DocumentItem[];
    total: number;
    documentCounts: Record<DocumentType, number>;
    meetingCounts: Record<"SUBMITTED" | "APPROVED" | "COMPLETED", number>;
    missingCount: number;
    missing: MissingMeeting[];
  }>(`management?${query}`);
  const pages = Math.max(1, Math.ceil(data.total / 50));
  const nextQuery = (next: number) => `/pengelolaan?page=${next}${q ? `&q=${encodeURIComponent(q)}` : ""}${type ? `&type=${type}` : ""}`;

  return <PageFrame>
    <PageTitle title="Dokumen & Evidence" description="Pantau kelengkapan dokumen rapat, notulensi, dan bukti pelaksanaan pascarapat." action={<span className="admin-console-badge"><ShieldCheck size={15}/> Akses Super User</span>}/>
    <section className="management-stats">
      <article><span><ClipboardList/></span><small>Menunggu persetujuan</small><strong>{data.meetingCounts.SUBMITTED}</strong></article>
      <article><span><FileCheck2/></span><small>Rapat disetujui</small><strong>{data.meetingCounts.APPROVED}</strong></article>
      <article><span><FileText/></span><small>Rapat selesai</small><strong>{data.meetingCounts.COMPLETED}</strong></article>
      <article className={data.missingCount ? "urgent" : ""}><span><AlertTriangle/></span><small>Evidence pascarapat belum lengkap</small><strong>{data.missingCount}</strong></article>
    </section>
    <section className="management-doc-types">{documentTypes.map(key => <article key={key}><span>{labels[key]}</span><strong>{data.documentCounts[key] ?? 0}</strong></article>)}</section>
    <section className="panel management-panel">
      <div className="panel-head"><div><h2>Rapat perlu ditindaklanjuti</h2><p>Dokumentasi dan daftar hadir wajib diunggah setelah rapat berakhir.</p></div><b className={data.missingCount ? "urgent-count" : "count-pill"}>{data.missingCount} perlu perhatian</b></div>
      {data.missing.length ? <div className="management-list">{data.missing.map(meeting => {
        const absent = [["DOCUMENTATION", "Dokumentasi"], ["ATTENDANCE_LIST", "daftar hadir"]].flatMap(([type, label]) => {
          const item = meeting.documents.find(doc => doc.documentType === type);
          return !item ? [`Kurang ${label}`] : !item.verified ? [`Menunggu verifikasi ${label}`] : [];
        });
        return <article key={meeting.id}><span className="management-alert-icon"><AlertTriangle size={17}/></span><div><a href={`/rapat/${meeting.id}`}><strong>{meeting.title}</strong></a><small>{meeting.department.name} · Penyelenggara {meeting.organizer.name} · berakhir {dateTime(meeting.endAt)}</small></div><b>{absent.join(" · ")}</b></article>;
      })}</div> : <p className="empty-state">Semua rapat yang berakhir sudah melengkapi evidence wajib.</p>}
    </section>
    <section className="panel management-panel">
      <div className="panel-head"><div><h2>Arsip Dokumen &amp; Evidence</h2><p>{data.total} dokumen · daftar dapat difilter dan ditelusuri.</p></div><a className="secondary-button" href="/rekap-bulanan">Buka Rekap Bulanan</a></div>
      <form className="management-filters"><input name="q" defaultValue={q} placeholder="Cari judul rapat"/><select name="type" defaultValue={type ?? ""}><option value="">Semua jenis dokumen</option>{documentTypes.map(key => <option value={key} key={key}>{labels[key]}</option>)}</select><button className="primary-button">Terapkan filter</button></form>
      <div className="management-table-wrap"><table className="management-table"><thead><tr><th>Dokumen</th><th>Judul Rapat</th><th>Bidang</th><th>Diunggah oleh</th><th>Waktu unggah</th><th>Verifikasi</th><th></th></tr></thead><tbody>{data.documents.map(doc => <tr key={doc.id}><td><span className={`document-type-tag ${doc.documentType.toLowerCase()}`}>{labels[doc.documentType]}</span><small>{doc.filename}</small></td><td><a href={`/rapat/${doc.meeting.id}`}>{doc.meeting.title}</a></td><td>{doc.meeting.department.name}</td><td>{doc.uploadedBy?.name ?? "Akun dihapus"}</td><td>{dateTime(doc.createdAt)}</td><td>{["DOCUMENTATION","ATTENDANCE_LIST"].includes(doc.documentType) ? <VerificationAction id={doc.id} verified={Boolean(doc.verifiedAt)}/> : "—"}</td><td><a className="document-open" href={`/api/documents/${doc.id}`}>Unduh</a></td></tr>)}</tbody></table>{!data.documents.length && <p className="empty-state">Tidak ada dokumen untuk filter ini.</p>}</div>
      <div className="account-pagination"><span>Halaman {page} dari {pages} · {data.total} dokumen</span><div><a className="secondary-button" aria-disabled={page <= 1} href={page > 1 ? nextQuery(page - 1) : undefined}>Sebelumnya</a><a className="secondary-button" aria-disabled={page >= pages} href={page < pages ? nextQuery(page + 1) : undefined}>Berikutnya</a></div></div>
    </section>
  </PageFrame>;
}
