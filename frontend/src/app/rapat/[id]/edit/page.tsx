import { notFound, redirect } from "next/navigation";
import { currentUser } from "@/lib/auth";
import { GoApiError, goApiFetch } from "@/lib/go-api";
import { PageFrame, PageTitle } from "@/app/components/page-frame";
import EditForm from "./edit-form";

export const dynamic = "force-dynamic";
type MeetingEditData = {
  id: string;
  title: string;
  description: string | null;
  startAt: string;
  endAt: string;
  onlineUrl: string | null;
  roomId: string | null;
  expectedParticipants: number;
  catering: string | null;
  notes: string | null;
  organizerId: string;
  status: string;
};

export default async function EditMeeting({ params }: { params: Promise<{ id: string }> }) {
  const auth = await currentUser();
  if (!auth) redirect("/login");
  const { id } = await params;
  let meeting: MeetingEditData;
  try {
    meeting = await goApiFetch<MeetingEditData>(`meetings/${encodeURIComponent(id)}`);
  } catch (error) {
    if (error instanceof GoApiError && error.status === 404) notFound();
    throw error;
  }
  const manager = ["ADMIN", "APPROVER"].includes(auth.user.role);
  const secretaryOwner = auth.user.role === "ORGANIZER" && meeting.organizerId === auth.user.id;
  if (!manager && !secretaryOwner) notFound();
  if (!["DRAFT", "SUBMITTED"].includes(meeting.status)) notFound();
  return <PageFrame>
    <PageTitle title="Edit Rapat" description="Perbarui detail jadwal dan dokumentasi rapat."/>
    <EditForm meeting={meeting}/>
  </PageFrame>;
}
