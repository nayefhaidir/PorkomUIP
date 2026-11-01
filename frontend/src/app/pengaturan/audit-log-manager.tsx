"use client";

import { useCallback, useEffect, useState } from "react";
import { Archive, ClipboardList, RefreshCw, Shield, Trash2 } from "lucide-react";
import { showToast } from "@/lib/client-notifications";

type LogItem = { id: string; action: string; entity: string; entityId: string; details: unknown; createdAt: string; user: { name: string; email: string } | null; meeting?: { id: string; meetingNumber: string; title: string } | null };
type Category = "system" | "meeting";

export default function AuditLogManager() {
  const [category, setCategory] = useState<Category>("system"), [items, setItems] = useState<LogItem[]>([]), [total, setTotal] = useState(0), [page, setPage] = useState(1), [busy, setBusy] = useState(false), [error, setError] = useState("");
  const pageSize = 25;
  const load = useCallback(async (targetPage = 1, targetCategory = category) => {
    setBusy(true); setError("");
    try {
      const response = await fetch(`/api/audit-logs?category=${targetCategory}&page=${targetPage}&pageSize=${pageSize}`, { cache: "no-store" });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data.error ?? "Log gagal dimuat.");
      setItems(data.items ?? []); setTotal(data.total ?? 0); setPage(targetPage);
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : "Log gagal dimuat.";
      setError(message);
    } finally { setBusy(false); }
  }, [category]);
  useEffect(() => { void load(1, category); }, [category, load]);
  async function purgeOldLogs() {
    if (!window.confirm("Hapus log sistem yang berusia lebih dari 365 hari? Log rapat tidak akan dihapus.")) return;
    setBusy(true);
    try {
      const response = await fetch("/api/audit-logs", { method: "DELETE", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ confirmation: "HAPUS LOG LAMA" }) });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data.error ?? "Log lama gagal dibersihkan.");
      showToast("Retensi log dijalankan", `${data.deleted} log sistem lama dihapus. Riwayat rapat tetap disimpan.`);
      await load(1, "system");
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : "Log lama gagal dibersihkan.";
      setError(message);
    } finally { setBusy(false); }
  }
  const dateLabel = (value: string) => new Intl.DateTimeFormat("id-ID", { dateStyle: "medium", timeStyle: "short", timeZone: "Asia/Jakarta" }).format(new Date(value));
  return <section className="panel settings-panel audit-log-panel">
    <div className="panel-head"><div><h2><ClipboardList size={18}/> Log &amp; Riwayat</h2><p className="muted">Log sistem dipertahankan 365 hari. Riwayat rapat terpisah dan tidak ikut pembersihan log sistem.</p></div><button type="button" className="secondary-button" disabled={busy} onClick={() => void load()}><RefreshCw size={14}/> Muat ulang</button></div>
    <div className="audit-log-toolbar"><div className="audit-log-tabs" role="tablist" aria-label="Kategori log"><button type="button" role="tab" aria-selected={category === "system"} className={category === "system" ? "active" : ""} onClick={() => setCategory("system")}><Shield size={14}/> Log Sistem</button><button type="button" role="tab" aria-selected={category === "meeting"} className={category === "meeting" ? "active" : ""} onClick={() => setCategory("meeting")}><ClipboardList size={14}/> Log Rapat</button></div><span>{total} catatan</span>{category === "system" && <button type="button" className="danger-button" disabled={busy} onClick={() => void purgeOldLogs()}><Trash2 size={14}/> Hapus log sistem &gt; 365 hari</button>}</div>
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="audit-log-list" aria-live="polite">{items.length ? items.map(item => { const details = item.details && typeof item.details === "object" ? item.details as Record<string, unknown> : null; const meetingTitle = item.meeting?.title ?? (typeof details?.meetingTitle === "string" ? details.meetingTitle : null); return <article className="audit-log-row" key={item.id}><div className="audit-log-icon">{category === "meeting" ? <ClipboardList size={16}/> : <Archive size={16}/>}</div><div className="audit-log-main"><div className="audit-log-heading"><strong>{item.action.replaceAll("_", " ")}</strong><time>{dateLabel(item.createdAt)}</time></div><p>{meetingTitle ?? item.entity}</p><small>{item.user?.name ?? "Akun telah dihapus"}{item.user?.email ? ` · ${item.user.email}` : ""} · {item.entityId}</small>{Boolean(item.details) && <details><summary>Lihat detail</summary><pre>{JSON.stringify(item.details, null, 2)}</pre></details>}</div></article>}) : <p className="empty-state">{busy ? "Memuat log…" : "Belum ada catatan log pada kategori ini."}</p>}</div>
    {total > pageSize && <div className="audit-log-pagination"><button type="button" className="secondary-button" disabled={busy || page <= 1} onClick={() => void load(page - 1)}>Sebelumnya</button><span>Halaman {page} dari {Math.ceil(total / pageSize)}</span><button type="button" className="secondary-button" disabled={busy || page * pageSize >= total} onClick={() => void load(page + 1)}>Berikutnya</button></div>}
  </section>;
}
