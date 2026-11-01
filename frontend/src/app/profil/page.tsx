import { currentUser } from "@/lib/auth";
import { redirect } from "next/navigation";
import { PageFrame,PageTitle } from "@/app/components/page-frame";
import ProfileForm from "./profile-form";
export default async function ProfilePage(){const auth=await currentUser();if(!auth)redirect("/login");return <PageFrame><PageTitle title="Profil" description="Perbarui nama dan keamanan akun Anda."/><ProfileForm user={{name:auth.user.name,email:auth.user.email,department:auth.user.department?.name??null,role:auth.user.role}}/></PageFrame>}
