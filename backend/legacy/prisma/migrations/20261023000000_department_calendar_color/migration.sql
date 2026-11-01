ALTER TABLE "Department" ADD COLUMN "color" TEXT NOT NULL DEFAULT '#2563EB';
UPDATE "Department" SET "color" = CASE
  WHEN lower("name") LIKE '%operasi konstruksi 1%' THEN '#16A36A'
  WHEN lower("name") LIKE '%pra konstruksi%' THEN '#8054D8'
  WHEN lower("name") LIKE '%operasi konstruksi 2%' THEN '#E49A22'
  WHEN lower("name") LIKE '%keuangan%' THEN '#1769E0'
  WHEN lower("name") LIKE '%k3%' THEN '#0796A5'
  ELSE '#2563EB'
END;
