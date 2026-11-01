ALTER TYPE "DocumentType" ADD VALUE 'ATTENDANCE_LIST';

ALTER TABLE "Meeting" ADD COLUMN "evidenceReminderSentAt" TIMESTAMP(3);

CREATE TABLE "MeetingGuest" (
    "id" TEXT NOT NULL,
    "meetingId" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    "email" TEXT,
    "organization" TEXT,
    CONSTRAINT "MeetingGuest_pkey" PRIMARY KEY ("id")
);

CREATE INDEX "MeetingGuest_meetingId_name_idx" ON "MeetingGuest"("meetingId", "name");
CREATE INDEX "Meeting_status_endAt_idx" ON "Meeting"("status", "endAt");

ALTER TABLE "MeetingGuest" ADD CONSTRAINT "MeetingGuest_meetingId_fkey"
    FOREIGN KEY ("meetingId") REFERENCES "Meeting"("id") ON DELETE CASCADE ON UPDATE CASCADE;
