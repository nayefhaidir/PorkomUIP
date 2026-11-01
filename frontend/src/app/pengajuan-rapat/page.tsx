import { PageFrame, PageTitle } from "@/app/components/page-frame";
import MeetingRequestForm from "./meeting-request-form";
import { currentUser } from "@/lib/auth";
import { redirect } from "next/navigation";
import { hasPageAccess } from "@/lib/auth";
export default async function RequestPage(){const auth=await currentUser();if(!auth)redirect("/login");const canSubmit=hasPageAccess(auth.user,"pengajuan",auth.user.role==="ADMIN"||auth.user.role==="ORGANIZER");return <PageFrame><PageTitle title="Pengajuan Rapat" description="Ajukan jadwal rapat dan kebutuhan peserta, ruang, serta konsumsi."/>{!canSubmit?<section className="panel"><h2>Akses lihat saja</h2><p>Akses pengajuan diatur oleh Super User untuk akun sekretariat yang ditunjuk.</p><a className="secondary-button" href="/kalender-rapat">Buka Kalender Rapat</a></section>:<MeetingRequestForm/>}</PageFrame>}
