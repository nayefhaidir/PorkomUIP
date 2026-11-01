CREATE TYPE "MeetingMethod" AS ENUM ('OFFLINE', 'ONLINE', 'HYBRID');
CREATE TYPE "GuestCategory" AS ENUM ('REGULAR', 'VIP');
CREATE TYPE "MeetingRequirementType" AS ENUM ('MIC', 'MC', 'DOCUMENTATION', 'PRESS_RELEASE', 'PROJECTOR', 'WHITEBOARD', 'OTHER');
CREATE TYPE "ConsumptionType" AS ENUM ('SNACK_BOX', 'RICE_BOX', 'COFFEE_BREAK', 'LUNCH', 'OTHER');
CREATE TYPE "DocumentType" AS ENUM ('SUPPORTING', 'DOCUMENTATION', 'MINUTES');
CREATE TYPE "ApprovalDecision" AS ENUM ('APPROVED', 'REJECTED');

ALTER TABLE "User" ADD COLUMN "jobTitle" TEXT, ADD COLUMN "isActive" BOOLEAN NOT NULL DEFAULT true, ADD COLUMN "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE "Room" ADD COLUMN "description" TEXT, ADD COLUMN "imageUrl" TEXT, ADD COLUMN "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, ADD COLUMN "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE "Meeting" ADD COLUMN "meetingNumber" TEXT, ADD COLUMN "method" "MeetingMethod" NOT NULL DEFAULT 'OFFLINE', ADD COLUMN "guestCategory" "GuestCategory" NOT NULL DEFAULT 'REGULAR', ADD COLUMN "timezone" TEXT NOT NULL DEFAULT 'Asia/Jakarta', ADD COLUMN "onlineResourceId" TEXT, ADD COLUMN "breakoutRoomCount" INTEGER NOT NULL DEFAULT 0, ADD COLUMN "submittedAt" TIMESTAMP(3), ADD COLUMN "approvedAt" TIMESTAMP(3), ADD COLUMN "cancelledAt" TIMESTAMP(3);
UPDATE "Meeting" SET "meetingNumber" = 'UIP-' || "id" WHERE "meetingNumber" IS NULL;
ALTER TABLE "Meeting" ALTER COLUMN "meetingNumber" SET NOT NULL;
CREATE UNIQUE INDEX "Meeting_meetingNumber_key" ON "Meeting"("meetingNumber");
ALTER TABLE "Meeting" ADD CONSTRAINT "Meeting_onlineResourceId_fkey" FOREIGN KEY ("onlineResourceId") REFERENCES "Room"("id") ON DELETE SET NULL ON UPDATE CASCADE;
ALTER TABLE "Meeting" ADD CONSTRAINT "Meeting_no_online_overlap" EXCLUDE USING gist ("onlineResourceId" WITH =, tsrange("startAt", "endAt", '[)') WITH &&) WHERE ("onlineResourceId" IS NOT NULL AND "status" IN ('SUBMITTED', 'APPROVED'));

CREATE TABLE "MeetingRequirement" (
  "id" TEXT NOT NULL, "meetingId" TEXT NOT NULL, "type" "MeetingRequirementType" NOT NULL, "detail" TEXT,
  CONSTRAINT "MeetingRequirement_pkey" PRIMARY KEY ("id")
);
CREATE UNIQUE INDEX "MeetingRequirement_meetingId_type_key" ON "MeetingRequirement"("meetingId", "type");
ALTER TABLE "MeetingRequirement" ADD CONSTRAINT "MeetingRequirement_meetingId_fkey" FOREIGN KEY ("meetingId") REFERENCES "Meeting"("id") ON DELETE CASCADE ON UPDATE CASCADE;

CREATE TABLE "MeetingConsumption" (
  "id" TEXT NOT NULL, "meetingId" TEXT NOT NULL, "itemType" "ConsumptionType" NOT NULL, "quantity" INTEGER NOT NULL, "unit" TEXT NOT NULL DEFAULT 'box',
  CONSTRAINT "MeetingConsumption_pkey" PRIMARY KEY ("id"), CONSTRAINT "MeetingConsumption_quantity_check" CHECK ("quantity" >= 0)
);
CREATE UNIQUE INDEX "MeetingConsumption_meetingId_itemType_key" ON "MeetingConsumption"("meetingId", "itemType");
CREATE INDEX "MeetingConsumption_itemType_meetingId_idx" ON "MeetingConsumption"("itemType", "meetingId");
ALTER TABLE "MeetingConsumption" ADD CONSTRAINT "MeetingConsumption_meetingId_fkey" FOREIGN KEY ("meetingId") REFERENCES "Meeting"("id") ON DELETE CASCADE ON UPDATE CASCADE;

CREATE TABLE "BreakoutRoom" (
  "id" TEXT NOT NULL, "meetingId" TEXT NOT NULL, "name" TEXT NOT NULL, "capacity" INTEGER NOT NULL,
  CONSTRAINT "BreakoutRoom_pkey" PRIMARY KEY ("id"), CONSTRAINT "BreakoutRoom_capacity_check" CHECK ("capacity" > 0)
);
CREATE UNIQUE INDEX "BreakoutRoom_meetingId_name_key" ON "BreakoutRoom"("meetingId", "name");
ALTER TABLE "BreakoutRoom" ADD CONSTRAINT "BreakoutRoom_meetingId_fkey" FOREIGN KEY ("meetingId") REFERENCES "Meeting"("id") ON DELETE CASCADE ON UPDATE CASCADE;

CREATE TABLE "MeetingApproval" (
  "id" TEXT NOT NULL, "meetingId" TEXT NOT NULL, "approverId" TEXT NOT NULL, "decision" "ApprovalDecision" NOT NULL, "comment" TEXT, "decidedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT "MeetingApproval_pkey" PRIMARY KEY ("id")
);
CREATE INDEX "MeetingApproval_meetingId_decidedAt_idx" ON "MeetingApproval"("meetingId", "decidedAt");
ALTER TABLE "MeetingApproval" ADD CONSTRAINT "MeetingApproval_meetingId_fkey" FOREIGN KEY ("meetingId") REFERENCES "Meeting"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "MeetingApproval" ADD CONSTRAINT "MeetingApproval_approverId_fkey" FOREIGN KEY ("approverId") REFERENCES "User"("id") ON DELETE RESTRICT ON UPDATE CASCADE;

ALTER TABLE "Document" ADD COLUMN "documentType" "DocumentType" NOT NULL DEFAULT 'SUPPORTING', ADD COLUMN "uploadedById" TEXT;
ALTER TABLE "Document" ADD CONSTRAINT "Document_uploadedById_fkey" FOREIGN KEY ("uploadedById") REFERENCES "User"("id") ON DELETE SET NULL ON UPDATE CASCADE;
CREATE TABLE "SystemSetting" (
  "key" TEXT NOT NULL, "value" JSONB NOT NULL, "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT "SystemSetting_pkey" PRIMARY KEY ("key")
);
