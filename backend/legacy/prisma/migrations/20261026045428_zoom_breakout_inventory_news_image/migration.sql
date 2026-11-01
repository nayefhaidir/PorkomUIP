-- AlterTable
ALTER TABLE "Announcement" ADD COLUMN     "imageMimeType" TEXT,
ADD COLUMN     "imageStorageKey" TEXT;

-- CreateTable
CREATE TABLE "ZoomBreakoutRoom" (
    "id" TEXT NOT NULL,
    "resourceId" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    "capacity" INTEGER NOT NULL DEFAULT 30,
    "active" BOOLEAN NOT NULL DEFAULT true,

    CONSTRAINT "ZoomBreakoutRoom_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "MeetingZoomBreakoutBooking" (
    "meetingId" TEXT NOT NULL,
    "breakoutRoomId" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "MeetingZoomBreakoutBooking_pkey" PRIMARY KEY ("meetingId","breakoutRoomId")
);

-- CreateIndex
CREATE INDEX "ZoomBreakoutRoom_resourceId_active_idx" ON "ZoomBreakoutRoom"("resourceId", "active");

-- CreateIndex
CREATE UNIQUE INDEX "ZoomBreakoutRoom_resourceId_name_key" ON "ZoomBreakoutRoom"("resourceId", "name");

-- CreateIndex
CREATE INDEX "MeetingZoomBreakoutBooking_breakoutRoomId_meetingId_idx" ON "MeetingZoomBreakoutBooking"("breakoutRoomId", "meetingId");

-- AddForeignKey
ALTER TABLE "ZoomBreakoutRoom" ADD CONSTRAINT "ZoomBreakoutRoom_resourceId_fkey" FOREIGN KEY ("resourceId") REFERENCES "Room"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "MeetingZoomBreakoutBooking" ADD CONSTRAINT "MeetingZoomBreakoutBooking_meetingId_fkey" FOREIGN KEY ("meetingId") REFERENCES "Meeting"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "MeetingZoomBreakoutBooking" ADD CONSTRAINT "MeetingZoomBreakoutBooking_breakoutRoomId_fkey" FOREIGN KEY ("breakoutRoomId") REFERENCES "ZoomBreakoutRoom"("id") ON DELETE RESTRICT ON UPDATE CASCADE;

-- Materialize each configured Zoom breakout as an individually bookable room.
INSERT INTO "ZoomBreakoutRoom" ("id", "resourceId", "name", "capacity", "active")
SELECT 'zoomslot-' || room."id" || '-' || slot.n,
       room."id",
       'Breakout ' || slot.n,
       GREATEST(1, CEIL(room."capacity"::numeric / GREATEST(room."breakoutRooms", 1)))::integer,
       true
FROM "Room" AS room
CROSS JOIN LATERAL generate_series(1, room."breakoutRooms") AS slot(n)
WHERE room."kind" = 'ONLINE' AND room."onlinePlatform" = 'ZOOM';

-- Keep historical Zoom bookings visible against the newly normalized slot inventory.
INSERT INTO "MeetingZoomBreakoutBooking" ("meetingId", "breakoutRoomId")
SELECT meeting."id", slot."id"
FROM "Meeting" AS meeting
JOIN "Room" AS resource ON resource."id" = meeting."onlineResourceId"
JOIN "BreakoutRoom" AS legacy ON legacy."meetingId" = meeting."id"
JOIN "ZoomBreakoutRoom" AS slot ON slot."resourceId" = resource."id" AND slot."name" = legacy."name"
WHERE resource."onlinePlatform" = 'ZOOM'
ON CONFLICT ("meetingId", "breakoutRoomId") DO NOTHING;
