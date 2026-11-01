"use client";

import { useEffect, useState, type FormEvent } from "react";
import { Mail, RotateCcw, Send, ShieldCheck } from "lucide-react";
import { showToast } from "@/lib/client-notifications";

type EventKey = "submission" | "status" | "invite" | "catering" | "evidence" | "evidenceUploaded" | "announcement" | "reschedule" | "beforeMeeting" | "verification" | "systemAlert";
type MailTemplate = { subject: string; body: string };
type OutboxFailure = { id: string; event: string; recipient: string; subject: string; attempts: number; lastError: string | null; createdAt: string };
type ApiFailure = Error & { reference?: string; apiFailure?: true };
type ApiResult = { error?: unknown; message?: unknown; details?: { reference?: unknown }; items?: unknown; requeued?: number; [key: string]: unknown };
type EmailSettingsData = {
  enabled: boolean;
  host: string;
  port: number;
  secure: boolean;
  username: string;
  fromName: string;
  fromEmail: string;
  facilitiesEmail: string;
  adminEmail: string;
  evidenceEscalationEmail: string;
  evidenceReminderHours: number[];
  preMeetingReminderMinutes: number[];
  hasPassword: boolean;
  encryptionReady: boolean;
  events: Record<EventKey, boolean>;
  templates: Record<EventKey, MailTemplate>;
  queue: { pending: number; failed: number; sent: number };
  [key: string]: unknown;
};

const eventLabels: Record<EventKey, string> = {
  submission: "Pengajuan baru ke approver",
  status: "Status rapat ke pembuat/peserta",
  invite: "Undangan peserta rapat",
  catering: "Permintaan konsumsi ke Sarana Umum",
  evidence: "Pengingat evidence setelah rapat",
  evidenceUploaded: "Evidence baru diunggah",
  announcement: "Pengumuman portal",
  reschedule: "Perubahan jadwal rapat",
  beforeMeeting: "Pengingat sebelum rapat",
  verification: "Evidence diverifikasi",
  systemAlert: "Email gagal setelah percobaan ulang",
};
const eventKeys = Object.keys(eventLabels) as EventKey[];

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function failureMessage(cause: unknown, fallback: string) {
  return cause instanceof Error && "apiFailure" in cause && cause.apiFailure === true ? cause.message : fallback;
}

function failureReference(cause: unknown) {
  return cause instanceof Error && "reference" in cause && typeof cause.reference === "string" ? cause.reference : "";
}

function isEmailSettingsData(value: ApiResult): value is ApiResult & EmailSettingsData {
  const queue = value.queue;
  const events = value.events;
  const templates = value.templates;
  return typeof value.enabled === "boolean" &&
    typeof value.host === "string" && typeof value.port === "number" &&
    typeof value.secure === "boolean" && typeof value.username === "string" &&
    typeof value.fromName === "string" && typeof value.fromEmail === "string" &&
    typeof value.facilitiesEmail === "string" && typeof value.adminEmail === "string" &&
    typeof value.evidenceEscalationEmail === "string" &&
    Array.isArray(value.evidenceReminderHours) && value.evidenceReminderHours.every(Number.isFinite) &&
    Array.isArray(value.preMeetingReminderMinutes) && value.preMeetingReminderMinutes.every(Number.isFinite) &&
    typeof value.hasPassword === "boolean" && typeof value.encryptionReady === "boolean" &&
    isRecord(queue) && typeof queue.pending === "number" && typeof queue.failed === "number" && typeof queue.sent === "number" &&
    isRecord(events) && eventKeys.every(key => typeof events[key] === "boolean") &&
    isRecord(templates) && eventKeys.every(key => isRecord(templates[key]) && typeof templates[key].subject === "string" && typeof templates[key].body === "string");
}

function isOutboxFailure(value: unknown): value is OutboxFailure {
  return isRecord(value) && typeof value.id === "string" &&
    typeof value.event === "string" && typeof value.recipient === "string" &&
    typeof value.subject === "string" && typeof value.attempts === "number" &&
    (value.lastError === null || typeof value.lastError === "string") &&
    typeof value.createdAt === "string";
}

export default function EmailSettings() {
  const [data, setData] = useState<EmailSettingsData | null>(null);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [errorReference, setErrorReference] = useState("");
  const [failures, setFailures] = useState<OutboxFailure[]>([]);

  async function responseData<T extends ApiResult = ApiResult>(response: Response, fallback: string): Promise<T> {
    let result: ApiResult;
    try {
      const payload: unknown = await response.json();
      if (!payload || typeof payload !== "object" || Array.isArray(payload)) throw new Error(fallback);
      result = payload as ApiResult;
    } catch {
      throw new Error(fallback);
    }
    if (!response.ok) {
      const failure = new Error(typeof result.error === "string" ? result.error : fallback) as ApiFailure;
      failure.apiFailure = true;
      if (typeof result.details?.reference === "string") failure.reference = result.details.reference;
      throw failure;
    }
    return result as T;
  }

  async function load() {
    const [configResponse, queueResponse] = await Promise.all([
      fetch("/api/email/config", { cache: "no-store" }),
      fetch("/api/email/outbox?status=FAILED&pageSize=50", { cache: "no-store" }),
    ]);
    const [config, queue] = await Promise.all([
      responseData<EmailSettingsData>(configResponse, "Konfigurasi email gagal dimuat."),
      responseData(queueResponse, "Antrean email gagal dimuat."),
    ]);
    if (!isEmailSettingsData(config) || !Array.isArray(queue.items) || !queue.items.every(isOutboxFailure)) {
      throw new Error("Respons pengaturan email dari backend tidak valid. Hubungi administrator jika masalah berlanjut.");
    }
    setData(config);
    setFailures(queue.items);
  }

  useEffect(() => {
    void load().catch(cause => setError(failureMessage(cause, "Konfigurasi email gagal dimuat. Periksa koneksi backend lalu coba lagi.")));
  }, []);

  const update = (fn: (current: EmailSettingsData) => EmailSettingsData) => setData(current => current ? fn(current) : current);

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!data || busy) return;
    setBusy(true);
    setError("");
    setErrorReference("");
    try {
      await saveCurrentConfiguration();
      showToast("Pengaturan email tersimpan", "Konfigurasi email dan kebijakan notifikasi berhasil diperbarui.");
      await load();
    } catch (cause) {
      setError(failureMessage(cause, "Pengaturan gagal disimpan. Periksa koneksi backend lalu coba lagi."));
      setErrorReference(failureReference(cause));
    } finally {
      setBusy(false);
    }
  }

  async function saveCurrentConfiguration() {
    if (!data) throw new Error("Konfigurasi email belum selesai dimuat.");
    const response = await fetch("/api/email/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        enabled: data.enabled,
        host: data.host,
        port: data.port,
        secure: data.secure,
        username: data.username,
        fromName: data.fromName,
        fromEmail: data.fromEmail,
        facilitiesEmail: data.facilitiesEmail,
        adminEmail: data.adminEmail,
        evidenceEscalationEmail: data.evidenceEscalationEmail,
        evidenceReminderHours: data.evidenceReminderHours,
        preMeetingReminderMinutes: data.preMeetingReminderMinutes,
        events: data.events,
        templates: data.templates,
        password: password || undefined,
      }),
    });
    const result = await responseData(response, "Pengaturan email tidak dapat disimpan.");
    setPassword("");
    update(current => ({ ...current, hasPassword: Boolean(password) || current.hasPassword }));
    return result;
  }

  async function run(path: string) {
    setBusy(true);
    setError("");
    setErrorReference("");
    try {
      if (path === "/api/email/test") await saveCurrentConfiguration();
      const response = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
      const result = await responseData(response, "Operasi email gagal.");
      const message = typeof result.message === "string" ? result.message : `${result.requeued ?? 0} email gagal dijadwalkan ulang.`;
      showToast("Operasi email selesai", message);
      await load();
    } catch (cause) {
      setError(failureMessage(cause, "Operasi email gagal. Periksa koneksi backend lalu coba lagi."));
      setErrorReference(failureReference(cause));
    } finally {
      setBusy(false);
    }
  }

  function failureLabel(code: string | null) {
    const messages: Record<string, string> = {
      MAIL_SMTP_DNS_FAILED: "Host SMTP tidak ditemukan; periksa host dan DNS.",
      MAIL_SMTP_PRIVATE_TARGET: "Relay SMTP privat tidak diizinkan oleh konfigurasi server.",
      MAIL_SMTP_TIMEOUT: "Koneksi SMTP melewati batas waktu; periksa firewall dan port.",
      MAIL_SMTP_CONNECTION_FAILED: "Server aplikasi tidak dapat tersambung ke SMTP.",
      MAIL_SMTP_STARTTLS_UNAVAILABLE: "Server SMTP tidak menawarkan STARTTLS.",
      MAIL_SMTP_TLS_FAILED: "Negosiasi TLS gagal; periksa mode keamanan dan sertifikat.",
      MAIL_SMTP_AUTH_FAILED: "Autentikasi SMTP ditolak; periksa username dan kata sandi.",
      MAIL_SMTP_SENDER_REJECTED: "Server SMTP menolak alamat pengirim.",
      MAIL_SMTP_RECIPIENT_REJECTED: "Server SMTP menolak alamat penerima.",
      MAIL_SMTP_MESSAGE_REJECTED: "Server SMTP menolak pesan.",
      MAIL_SMTP_TRANSFER_FAILED: "Transfer pesan ke SMTP gagal.",
      MAIL_SMTP_ACCEPTANCE_FAILED: "Server SMTP tidak mengonfirmasi penerimaan pesan.",
      MAIL_CREDENTIALS_UNAVAILABLE: "Kredensial SMTP tidak dapat dibuka; periksa kunci enkripsi deployment.",
      MAIL_ENCRYPTION_KEY_MISSING: "Kunci enkripsi tidak tersedia pada layanan backend.",
      MAIL_CONFIG_INVALID: "Konfigurasi SMTP tidak valid.",
    };
    return code ? messages[code] ?? "Penyebab teknis tidak tersedia; coba ulang atau periksa log server." : "Tidak tersedia";
  }

  if (!data) return <section className="panel settings-panel"><h2><Mail/> Email &amp; notifikasi</h2><p>{error || "Memuat konfigurasi email…"}</p></section>;

  return <section className="panel settings-panel email-settings">
    <div className="panel-head"><h2><Mail/> Email &amp; notifikasi</h2><span className="email-queue-summary">Antrean {data.queue.pending} · Gagal {data.queue.failed} · Diterima SMTP {data.queue.sent}</span></div>
    <p className="muted">Email diproses oleh worker latar belakang; permintaan rapat tidak menunggu SMTP. Status diterima berarti server SMTP menerima pesan, bukan konfirmasi masuk ke kotak penerima.</p>
    {!data.encryptionReady && <p className="email-security-note"><ShieldCheck size={16}/> Tambahkan <code>MAIL_ENCRYPTION_KEY</code> berisi 64 karakter heksadesimal pada environment backend sebelum menyimpan kata sandi SMTP.</p>}
    {error && <div className="form-error" role="alert"><p>{error}</p>{errorReference && <small>Referensi untuk administrator: <code>{errorReference}</code></small>}</div>}
    <form onSubmit={save} className="email-config-form">
      <label className="setting-toggle"><input type="checkbox" checked={data.enabled} onChange={event => update(current => ({ ...current, enabled: event.target.checked }))}/><span>Aktifkan pengiriman email</span></label>
      <div className="email-fields">
        <label>Host SMTP<input required value={data.host} onChange={event => update(current => ({ ...current, host: event.target.value }))} placeholder="smtp.perusahaan.co.id"/></label>
        <label>Port<input required type="number" min="1" max="65535" value={data.port} onChange={event => update(current => ({ ...current, port: Number(event.target.value) }))}/></label>
        <label>Keamanan<select value={data.secure ? "ssl" : "starttls"} onChange={event => update(current => ({ ...current, secure: event.target.value === "ssl" }))}><option value="starttls">STARTTLS</option><option value="ssl">SSL/TLS</option></select></label>
        <label>Username SMTP<input autoComplete="username" value={data.username} onChange={event => update(current => ({ ...current, username: event.target.value }))}/></label>
        <label>Kata sandi SMTP<input autoComplete="new-password" type="password" value={password} onChange={event => setPassword(event.target.value)} placeholder={data.hasPassword ? "Tersimpan aman; kosongkan jika tidak diubah" : "Masukkan kata sandi SMTP"}/></label>
        <label>Nama pengirim<input required maxLength={100} value={data.fromName} onChange={event => update(current => ({ ...current, fromName: event.target.value }))}/></label>
        <label>Email pengirim<input required type="email" value={data.fromEmail} onChange={event => update(current => ({ ...current, fromEmail: event.target.value }))}/></label>
      </div>
      <div className="email-fields">
        <label>Email Sarana Umum<input type="email" value={data.facilitiesEmail} onChange={event => update(current => ({ ...current, facilitiesEmail: event.target.value }))} placeholder="sarana.umum@perusahaan.co.id"/><small>Penerima tetap untuk kebutuhan konsumsi.</small></label>
        <label>Email Administrator<input type="email" value={data.adminEmail} onChange={event => update(current => ({ ...current, adminEmail: event.target.value }))} placeholder="admin@perusahaan.co.id"/><small>Penerima tetap untuk notifikasi sistem.</small></label>
        <label>Email Eskalasi Evidence<input type="email" value={data.evidenceEscalationEmail} onChange={event => update(current => ({ ...current, evidenceEscalationEmail: event.target.value }))} placeholder="supervisor@perusahaan.co.id"/><small>Penerima tetap pada interval eskalasi terakhir.</small></label>
      </div>
      <div className="email-fields">
        <label>Pengingat evidence (jam setelah rapat, pisahkan koma)<input value={data.evidenceReminderHours.join(", ")} onChange={event => update(current => ({ ...current, evidenceReminderHours: event.target.value.split(",").map(value => Number(value.trim())).filter(Number.isFinite) }))} placeholder="1, 3, 7"/></label>
        <label>Pengingat rapat (menit sebelum mulai, pisahkan koma)<input value={data.preMeetingReminderMinutes.join(", ")} onChange={event => update(current => ({ ...current, preMeetingReminderMinutes: event.target.value.split(",").map(value => Number(value.trim())).filter(Number.isFinite) }))} placeholder="60"/></label>
      </div>
      <div className="email-event-options"><strong>Jenis email yang dikirim</strong>{eventKeys.map(key => <label key={key}><input type="checkbox" checked={data.events[key]} onChange={event => update(current => ({ ...current, events: { ...current.events, [key]: event.target.checked } }))}/>{eventLabels[key]}</label>)}</div>
      <p className="muted">Penerima pembuat, approver, peserta, dan pengumuman ditentukan dari data pengguna/rapat. Pengaturan tidak menerima alamat global untuk mengganti penerima dinamis.</p>
      <details className="email-template-details"><summary>Atur template email per jenis notifikasi</summary><p className="muted">Placeholder yang tersedia mengikuti event, termasuk <code>{"{{meetingTitle}}"}</code>, <code>{"{{meetingNumber}}"}</code>, <code>{"{{meetingDetails}}"}</code>, <code>{"{{status}}"}</code>, <code>{"{{reason}}"}</code>, <code>{"{{consumption}}"}</code>, dan <code>{"{{missingEvidence}}"}</code>. Konten HTML di-escape sebelum dikirim.</p>{eventKeys.map(key => <fieldset key={key}><legend>{eventLabels[key]}</legend><label>Subjek<input maxLength={180} value={data.templates[key].subject} onChange={event => update(current => ({ ...current, templates: { ...current.templates, [key]: { ...current.templates[key], subject: event.target.value } } }))}/></label><label>Isi email<textarea rows={5} maxLength={4000} value={data.templates[key].body} onChange={event => update(current => ({ ...current, templates: { ...current.templates, [key]: { ...current.templates[key], body: event.target.value } } }))}/></label></fieldset>)}</details>
      <div className="inline-actions"><button className="primary-button" disabled={busy}>{busy ? "Menyimpan…" : "Simpan Pengaturan"}</button><button type="button" className="secondary-button" disabled={busy} onClick={() => void run("/api/email/test")}><Send size={14}/> Kirim Email Uji ke Akun Saya</button><button type="button" className="secondary-button" disabled={busy || data.queue.failed === 0} onClick={() => void run("/api/email/retry")}><RotateCcw size={14}/> Jadwalkan Ulang Email Gagal ({data.queue.failed})</button></div>
    </form>
    <section className="management-panel"><div className="panel-head"><div><h3>Email gagal</h3><p className="muted">Pesan SMTP ditampilkan secara umum dan tidak memuat kredensial.</p></div><span>{failures.length} dari maksimal 50</span></div>{failures.length ? <div className="management-table-wrap"><table className="management-table"><thead><tr><th>Penerima</th><th>Peristiwa</th><th>Percobaan</th><th>Kegagalan terakhir</th><th>Waktu antre</th></tr></thead><tbody>{failures.map(item => <tr key={item.id}><td>{item.recipient}</td><td>{item.event.split(":")[0]}</td><td>{item.attempts}</td><td>{failureLabel(item.lastError)}</td><td>{new Intl.DateTimeFormat("id-ID", { dateStyle: "medium", timeStyle: "short" }).format(new Date(item.createdAt))}</td></tr>)}</tbody></table></div> : <p className="empty-state">Tidak ada email gagal.</p>}</section>
  </section>;
}
