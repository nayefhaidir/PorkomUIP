ALTER TABLE "Department" ADD COLUMN "shortName" TEXT NOT NULL DEFAULT '';
UPDATE "Department" SET "shortName" = CASE
  WHEN lower("name") LIKE '%operasi konstruksi 1%' THEN 'OPK 1'
  WHEN lower("name") LIKE '%operasi konstruksi 2%' THEN 'OPK 2'
  WHEN lower("name") LIKE '%pra konstruksi%' THEN 'PRA'
  WHEN lower("name") LIKE '%keuangan%' THEN 'KUSD'
  WHEN lower("name") LIKE '%keamanan%' OR lower("name") LIKE '%lingkungan%' THEN 'K3'
  ELSE upper(left(regexp_replace("name", '[^A-Za-z0-9]', '', 'g'), 8))
END;
CREATE UNIQUE INDEX "Department_shortName_key" ON "Department"("shortName");

CREATE TABLE "EmailOutbox" (
  "id" TEXT NOT NULL,
  "eventKey" TEXT NOT NULL,
  "recipient" TEXT NOT NULL,
  "subject" TEXT NOT NULL,
  "textBody" TEXT NOT NULL,
  "htmlBody" TEXT NOT NULL,
  "status" TEXT NOT NULL DEFAULT 'PENDING',
  "attempts" INTEGER NOT NULL DEFAULT 0,
  "lastError" TEXT,
  "availableAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "sentAt" TIMESTAMP(3),
  "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updatedAt" TIMESTAMP(3) NOT NULL,
  CONSTRAINT "EmailOutbox_pkey" PRIMARY KEY ("id")
);
CREATE UNIQUE INDEX "EmailOutbox_eventKey_key" ON "EmailOutbox"("eventKey");
CREATE INDEX "EmailOutbox_status_availableAt_idx" ON "EmailOutbox"("status", "availableAt");
