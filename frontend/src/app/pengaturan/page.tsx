import { currentUser } from "@/lib/auth";
import { redirect } from "next/navigation";
import { PageFrame,PageTitle } from "@/app/components/page-frame";
import AdminSettings from "./admin-settings";
import EmailSettings from "./email-settings";
export default async function SettingsPage(){const auth=await currentUser();if(!auth)redirect("/login");return <PageFrame><PageTitle title="Pengaturan Portal" description="Konfigurasi akun dan hak akses, bidang, ruangan, kebijakan persetujuan, serta email otomatis."/><AdminSettings role={auth.user.role}/>{auth.user.role==="ADMIN"&&<EmailSettings/>}</PageFrame>}
