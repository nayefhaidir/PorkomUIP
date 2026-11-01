"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { CalendarDays, Clock3, MapPin, Building2, Maximize2, Minimize2, Megaphone, Radio, ChevronLeft, ChevronRight } from "lucide-react";
type Meeting = { id: string; title: string; startAt: string; endAt: string; method: string; department: { name: string }; room: { name: string } | null; onlineResource: { name: string } | null };
type Announcement = { id: string; title: string; body: string; publishedAt: string };
type Feed = { generatedAt: string; meetings: Meeting[]; announcements: Announcement[] };
const dateFmt = new Intl.DateTimeFormat("id-ID", { weekday: "long", day: "2-digit", month: "long", year: "numeric", timeZone: "Asia/Jakarta" });
const timeFmt = new Intl.DateTimeFormat("id-ID", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, timeZone: "Asia/Jakarta" });
const isImportant = (item: Announcement) => /(penting|segera|urgent|darurat|perubahan|gangguan|pemeliharaan|ditutup|evakuasi)/i.test(item.title + " " + item.body);

export default function PublicScreen() {
  const [feed, setFeed] = useState<Feed>({ generatedAt: "", meetings: [], announcements: [] });
  const [now, setNow] = useState<Date | null>(null);
  const [error, setError] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [announcementOffset, setAnnouncementOffset] = useState(0);
  const [agendaPage,setAgendaPage]=useState(0);
  const refresh = useCallback(async () => {
    try { const response = await fetch("/api/public/schedule", { cache: "no-store" }); if (!response.ok) throw new Error("feed"); setFeed(await response.json()); setError(false); }
    catch { setError(true); }
  }, []);
  useEffect(() => {
    setNow(new Date()); void refresh();
    const poll = setInterval(() => void refresh(), 30000);
    const tick = setInterval(() => setNow(new Date()), 1000);
    const syncFullscreen = () => setFullscreen(Boolean(document.fullscreenElement));
    document.addEventListener("fullscreenchange", syncFullscreen);
    return () => { clearInterval(poll); clearInterval(tick); document.removeEventListener("fullscreenchange", syncFullscreen); };
  }, [refresh]);
  const orderedAnnouncements = useMemo(() => [...feed.announcements].sort((a, b) => Number(isImportant(b)) - Number(isImportant(a)) || Date.parse(b.publishedAt) - Date.parse(a.publishedAt)), [feed.announcements]);
  useEffect(() => {
    if (orderedAnnouncements.length < 3) { setAnnouncementOffset(0); return; }
    const timer = setInterval(() => setAnnouncementOffset(offset => (offset + 1) % orderedAnnouncements.length), 9000);
    return () => clearInterval(timer);
  }, [orderedAnnouncements.length]);
  async function toggleFullscreen() {
    try { if (document.fullscreenElement) await document.exitFullscreen(); else await document.documentElement.requestFullscreen(); }
    catch { setError(true); }
  }
  const today = now?.toLocaleDateString("en-CA", { timeZone: "Asia/Jakarta" }) ?? "";
  const todayMeetings = feed.meetings.filter(meeting => new Date(meeting.startAt).toLocaleDateString("en-CA", { timeZone: "Asia/Jakarta" }) === today);
  const agendaPageSize=6,agendaPages=Math.max(1,Math.ceil(todayMeetings.length/agendaPageSize));
  useEffect(()=>{if(agendaPage>=agendaPages)setAgendaPage(0)},[agendaPage,agendaPages]);
  useEffect(()=>{if(agendaPages<2)return;const timer=setInterval(()=>setAgendaPage(page=>(page+1)%agendaPages),12000);return()=>clearInterval(timer)},[agendaPages]);
  const visibleTodayMeetings=todayMeetings.slice(agendaPage*agendaPageSize,(agendaPage+1)*agendaPageSize);
  const upcoming = feed.meetings.filter(meeting => !todayMeetings.includes(meeting));
  const visibleAnnouncementIndices = orderedAnnouncements.length ? [announcementOffset, (announcementOffset + 1) % orderedAnnouncements.length].slice(0, Math.min(2, orderedAnnouncements.length)) : [];
  const visibleAnnouncements = visibleAnnouncementIndices.map(index => orderedAnnouncements[index]);
  const announcementRange = visibleAnnouncementIndices.map(index => index + 1).join(" & ");

  return <main className="public-screen">
    <header className="public-header"><div className="public-brand"><img src="/pln-indonesia-power.png" alt="PLN Indonesia Power"/><span><b>UNIT INDUK PROYEK</b><small>MEETING &amp; VISITOR INFORMATION</small></span></div><div className="public-header-status"><span className="public-live"><Radio size={15}/> LIVE</span><span>INFORMASI GEDUNG</span><button onClick={toggleFullscreen} aria-label={fullscreen ? "Keluar dari layar penuh" : "Masuk layar penuh"} title={fullscreen ? "Keluar layar penuh" : "Layar penuh"}>{fullscreen ? <Minimize2 size={18}/> : <Maximize2 size={18}/>}<span>{fullscreen ? "Keluar layar penuh" : "Layar penuh"}</span></button></div></header>
    <section className="public-hero"><div><p className="public-eyebrow">SELAMAT DATANG DI UNIT INDUK PROYEK</p><h1>Informasi Rapat</h1><p>Agenda ruang rapat dan pengumuman untuk seluruh pengunjung</p></div><div className="public-clock"><strong>{now ? timeFmt.format(now) : "--:--:--"}<small> WIB</small></strong><span>{now ? dateFmt.format(now) : "Memuat tanggal…"}</span></div></section>
    {error && <p className="public-warning">Data belum dapat diperbarui. Mencoba kembali otomatis.</p>}
    <section className="public-content"><div className="public-agenda"><header><h2><CalendarDays/> Agenda Hari Ini</h2><span>{todayMeetings.length} rapat</span></header>{todayMeetings.length === 0 ? <div className="public-empty">Tidak ada rapat terjadwal hari ini.</div> : <div className="public-agenda-list" aria-live="polite">{visibleTodayMeetings.map(meeting => <article className="public-meeting" key={meeting.id}><div className="public-time"><strong>{timeFmt.format(new Date(meeting.startAt))}</strong><span>{timeFmt.format(new Date(meeting.endAt))}</span></div><div className="public-meeting-info"><h3>{meeting.title}</h3><p><Building2/>{meeting.department.name}</p><p><MapPin/>{[meeting.room?.name, meeting.onlineResource?.name].filter(Boolean).join(" + ") || meeting.method}</p></div><span className="public-method">{meeting.method}</span></article>)}</div>}{agendaPages>1&&<div className="public-agenda-pagination"><span>Agenda {agendaPage*agendaPageSize+1}–{Math.min((agendaPage+1)*agendaPageSize,todayMeetings.length)} dari {todayMeetings.length}</span><div>{Array.from({length:agendaPages},(_,index)=><i key={index} className={index===agendaPage?"active":""}/>)}</div><small>berganti otomatis</small></div>}</div>
      <aside className="public-side"><section className="public-panel"><h2><Clock3/> Agenda Berikutnya</h2>{upcoming.length ? upcoming.slice(0, 8).map(meeting => <article className="public-upcoming" key={meeting.id}><span>{dateFmt.format(new Date(meeting.startAt))}</span><strong>{meeting.title}</strong><small>{timeFmt.format(new Date(meeting.startAt))}–{timeFmt.format(new Date(meeting.endAt))} · {meeting.room?.name ?? meeting.onlineResource?.name ?? meeting.method}</small></article>) : <p className="public-empty">Belum ada agenda berikutnya.</p>}</section>
        <section className="public-panel public-news" aria-live="polite"><h2><Megaphone/> Pengumuman <span className="public-news-count">{orderedAnnouncements.length} aktif</span></h2>{visibleAnnouncements.length ? <div className="public-news-slides">{visibleAnnouncements.map(item => <article className={isImportant(item) ? "public-announcement important" : "public-announcement"} key={item.id}><span className="public-news-badge">{isImportant(item) ? "PENTING" : "INFORMASI"}</span><strong>{item.title}</strong><p>{item.body}</p></article>)}</div> : <p className="public-empty">Belum ada pengumuman.</p>}{orderedAnnouncements.length > 2 && <div className="public-news-pagination"><button aria-label="Pengumuman sebelumnya" onClick={() => setAnnouncementOffset(offset => (offset - 1 + orderedAnnouncements.length) % orderedAnnouncements.length)}><ChevronLeft size={17}/></button><span>{announcementRange} dari {orderedAnnouncements.length} · berganti otomatis</span><div>{orderedAnnouncements.map((item, index) => <button key={item.id} aria-label={`Tampilkan pengumuman ${index + 1}`} className={index === announcementOffset ? "active" : ""} onClick={() => setAnnouncementOffset(index)}/>)}</div><button aria-label="Pengumuman berikutnya" onClick={() => setAnnouncementOffset(offset => (offset + 1) % orderedAnnouncements.length)}><ChevronRight size={17}/></button></div>}</section>
      </aside></section>
    <footer className="public-footer"><span>PLN Indonesia Power · Unit Induk Proyek</span><span>{error ? "Koneksi bermasalah" : "Informasi diperbarui otomatis · 30 detik"}</span></footer>
  </main>;
}
