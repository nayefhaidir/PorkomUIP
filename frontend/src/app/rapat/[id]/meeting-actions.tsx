"use client";
import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { Trash2, UploadCloud } from "lucide-react";
import { showToast } from "@/lib/client-notifications";

export function MeetingActions({ id, status, canReview, canCancel, canDelete }: { id: string; status: string; canReview: boolean; canCancel: boolean; canDelete: boolean }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
  async function act(action: string, reason?: string) {
    setBusy(true);
    try {
      const response = await fetch(`/api/meetings/${id}/workflow`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action, ...(reason ? { reason } : {}) }) });
      const data = await response.json();
      if (!response.ok) { const message=data.error??"Aksi gagal";showToast("Perubahan rapat gagal",message,"error");return; }
      showToast("Status rapat diperbarui",`Aksi ${action} berhasil diproses.`);
      router.refresh();
    } catch { const message="Tidak dapat menghubungi server. Coba lagi.";showToast("Perubahan rapat gagal",message,"error"); }
    finally { setBusy(false); }
  }
  async function removeMeeting() {
    if (!window.confirm("Hapus rapat ini secara permanen? Jadwal dan dokumen rapat akan dihapus, sedangkan log audit tetap disimpan.")) return;
    setDeleting(true);
    try {
      const response = await fetch(`/api/meetings/${id}`, { method: "DELETE" });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) { const message=data.error??"Rapat gagal dihapus.";showToast("Rapat gagal dihapus",message,"error");return; }
      showToast("Rapat dihapus",data.cleanupPending?"Rapat terhapus; sebagian berkas perlu pembersihan oleh administrator.":"Rapat dan dokumen terkait berhasil dihapus. Log audit tetap tersimpan.");
      router.replace("/kalender-rapat"); router.refresh();
    } catch { showToast("Rapat gagal dihapus","Server tidak dapat menyelesaikan penghapusan. Coba lagi.","error"); }
    finally { setDeleting(false); }
  }
  return <div className="workflow-actions">
    {status === "SUBMITTED" && canReview && <><button className="primary-button" disabled={busy} onClick={() => void act("approve")}>Setujui</button><form onSubmit={event => { event.preventDefault(); const reason = new FormData(event.currentTarget).get("reason") as string; void act("reject", reason); }}><input name="reason" required minLength={3} maxLength={1000} placeholder="Alasan penolakan"/><button className="danger-button" disabled={busy}>Tolak</button></form></>}
    {["DRAFT", "SUBMITTED", "APPROVED"].includes(status) && canCancel && <button className="secondary-button" disabled={busy||deleting} onClick={() => void act("cancel", "Dibatalkan oleh penyelenggara")}>Batalkan Rapat</button>}
    {canDelete && <button className="danger-button" disabled={busy||deleting} onClick={() => void removeMeeting()}><Trash2 size={15}/>{deleting?"Menghapus…":"Hapus Rapat"}</button>}
  </div>;
}

export function DocumentUpload({ id, allowed, afterMeeting }: { id: string; allowed: boolean; afterMeeting: boolean }) {
  const router=useRouter();
  const [busy, setBusy] = useState(false);
  async function upload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true);
    const form = new FormData(event.currentTarget);
    try {
      const response = await fetch(`/api/meetings/${id}/documents`, { method: "POST", body: form });
      const data = await response.json();
      if (!response.ok) { const message=data.error??"Upload gagal";showToast("Dokumen gagal diunggah",message,"error");return; }
      showToast("Dokumen berhasil diunggah",`${data.filename??"Dokumen rapat"} sudah tersimpan.`);
      router.refresh();
    } catch { const message="Upload gagal karena koneksi terputus. Coba lagi.";showToast("Dokumen gagal diunggah",message,"error"); }
    finally { setBusy(false); }
  }
  if (!allowed) return null;
  return <form className="upload-form evidence-upload" onSubmit={upload}>
    <div className="upload-form-heading"><UploadCloud size={18}/><div><strong>Tambah dokumen</strong><small>PDF, PNG, JPG, DOCX, XLSX · maks. 10 MB</small></div></div>
    <label>Jenis dokumen<select name="documentType" defaultValue={afterMeeting ? "DOCUMENTATION" : "SUPPORTING"}><option value="SUPPORTING">Dokumen pendukung</option>{afterMeeting && <><option value="DOCUMENTATION">Dokumentasi rapat</option><option value="ATTENDANCE_LIST">Daftar hadir</option><option value="MINUTES">Notulensi</option></>}</select></label>
    <label className="evidence-file-input">Pilih berkas<input type="file" name="file" accept=".pdf,.png,.jpg,.jpeg,.docx,.xlsx" required/></label>
    <button className="primary-button" disabled={busy}>{busy ? "Mengunggah…" : "Unggah dokumen"}</button>
  </form>;
}
