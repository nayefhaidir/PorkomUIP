CREATE TYPE "OnlinePlatform" AS ENUM ('ZOOM', 'TEAMS');
ALTER TABLE "Room" ADD COLUMN "onlinePlatform" "OnlinePlatform";
UPDATE "Room" SET "onlinePlatform" = 'ZOOM' WHERE "kind" = 'ONLINE';
