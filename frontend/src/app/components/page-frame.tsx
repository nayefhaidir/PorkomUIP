import { redirect } from "next/navigation";
import { currentUser } from "@/lib/auth";
import { AppShell } from "@/app/components/app-shell";
export async function PageFrame({ children, allowPublic = false }: { children: React.ReactNode; allowPublic?: boolean }) {
  const auth=await currentUser();if(!auth){if(allowPublic)return <AppShell publicMode user={{name:"Pengunjung",role:"PUBLIC",department:null,pageAccess:["beranda","kalender"]}}><div className="page-content">{children}</div></AppShell>;redirect("/login")}
  return <AppShell user={{name:auth.user.name,role:auth.user.role,department:auth.user.department?.name??null,pageAccess:auth.user.pageAccess}}><div className="page-content">{children}</div></AppShell>;
}
export function PageTitle({ title, description, action }: { title:string; description?:string; action?:React.ReactNode }) {
  return <div className="page-title"><div><h1>{title}</h1>{description&&<p>{description}</p>}</div>{action}</div>;
}
