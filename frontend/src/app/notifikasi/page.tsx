import { redirect } from "next/navigation";
import { currentUser } from "@/lib/auth";
import { goApiFetch } from "@/lib/go-api";
import { PageFrame, PageTitle } from "@/app/components/page-frame";
import NotificationsList from "./notifications-list";

export const dynamic = "force-dynamic";

type Notification = {
  id: string;
  title: string;
  body: string;
  readAt: string | null;
  createdAt: string;
};

export default async function NotificationsPage() {
  const auth = await currentUser();
  if (!auth) redirect("/login");
  const result = await goApiFetch<{ items: Notification[]; total: number; unread: number }>("notifications?page=1&pageSize=50");
  return <PageFrame>
    <PageTitle title="Notifikasi" description="Pemberitahuan rapat dan aktivitas yang memerlukan perhatian."/>
    <NotificationsList items={result.items} total={result.total} unread={result.unread}/>
  </PageFrame>;
}
