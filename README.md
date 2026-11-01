# UIP Meeting Portal

Portal internal Unit Induk Proyek untuk penjadwalan rapat, persetujuan, ruangan, peserta, dokumentasi, notulensi, notifikasi, dan rekap.

## Stack dan struktur

- Frontend Next.js App Router 15, React 19, TypeScript strict di `frontend/`.
- Backend Go REST API di `backend/`, dengan entrypoint `backend/cmd/api` dan lapisan domain, application, transport, serta SQL storage; frontend memproksikan `/api` tanpa mengakses database.
- PostgreSQL atau SQLite dipilih melalui `DATABASE_URL`; unggahan dokumen tetap disimpan di volume lokal persisten.
- Session JWT HttpOnly dengan hash session tersimpan di database, bcrypt untuk password, role `ADMIN`, `APPROVER`, `ORGANIZER`, dan `PARTICIPANT`.
- Backend Go memvalidasi payload, mengelola session dan audit, menyimpan dokumen secara persisten, serta menghasilkan laporan CSV/XLSX/PDF.

Route UI: `/login`, `/beranda`, `/kalender-rapat`, `/pengajuan-rapat`, `/rekap-bulanan`, `/ketersediaan-ruangan`, `/rapat/[id]`, `/rapat/[id]/edit`, `/notifikasi`, `/profil`, dan `/pengaturan`.

## Deploy dan operasional Docker

### Persiapan pertama

1. Pasang Docker Desktop atau Docker Engine beserta Docker Compose v2, OpenSSL, dan `curl`.
2. Buat konfigurasi lokal dengan `cp .env.example .env`. Jangan masukkan `.env` ke Git atau membagikan isinya.
3. Tetapkan `AUTH_SECRET` sebagai nilai acak minimal 32 karakter dan `DEMO_PASSWORD` sebagai password seed unik. Untuk PostgreSQL, tetapkan `POSTGRES_PASSWORD` sebagai nilai hex acak dan samakan password itu di `DATABASE_URL_DOCKER` (contoh URL: `postgres://uip:<password-hex>@db:5432/uip_meeting?sslmode=disable`). Nilai hex aman dipakai langsung pada URL. SQLite hanya memerlukan `AUTH_SECRET` dan `DEMO_PASSWORD`.
4. Atur `PUBLIC_APP_URL` ke alamat frontend yang dapat diakses pengguna melalui email, misalnya `http://localhost:3000` untuk uji lokal atau URL HTTPS deployment. Pengiriman email belum aktif sampai SMTP dikonfigurasi oleh administrator di **Pengaturan → Email & notifikasi**.
5. Untuk relay SMTP privat/internal, ubah `SMTP_ALLOW_PRIVATE_NETWORK=true` hanya jika jaringan dan relay tersebut memang dipercaya; default-nya memblokir target privat.

### Deploy atau update

Jalankan dari root proyek:

```sh
sh scripts/deploy.sh postgres
```

Mode `postgres` adalah default dan memakai `docker-compose.yml`. Untuk SQLite:

```sh
sh scripts/deploy.sh sqlite
```

Skrip membuat `MAIL_ENCRYPTION_KEY` acak 32 byte (64 karakter hex) pada file rahasia khusus `.mail-encryption.env` jika file tersebut belum ada. File ini diabaikan Git, diberi izin terbatas, dan tidak pernah ditampilkan; skrip tidak membaca atau mengubah isi `.env`. Key valid yang sudah ada dipertahankan, sedangkan file key yang tidak valid membuat deployment berhenti tanpa mengubahnya. Skrip kemudian membangun image, menunggu health check Compose, memverifikasi `/api/health` dan `/healthz`, lalu menampilkan status service. Skrip tidak menyalin konfigurasi dari file lain dan tidak menjalankan seed otomatis.

Jalankan seed **hanya sekali untuk instalasi baru/development**, bukan setiap update:

```sh
# PostgreSQL
docker compose exec api /app/uip-api seed

# SQLite
docker compose -f docker-compose.sqlite.yml exec api /app/uip-api seed
```

Akun seed development adalah `admin@uip.local` dan `approver@uip.local`, memakai `DEMO_PASSWORD`. Jangan gunakan akun seed di production. Buka frontend pada port 3000. API hanya dipublikasikan di loopback host pada port 8080; database PostgreSQL juga hanya di loopback port 5432.

### Pemeriksaan, log, dan berhenti

```sh
docker compose ps
docker compose logs --tail=100 app api db
curl --fail http://127.0.0.1:8080/api/health
curl --fail http://127.0.0.1:3000/healthz
```

Untuk konfigurasi SQLite, tambahkan `-f docker-compose.sqlite.yml` ke setiap perintah Compose. `sh scripts/deploy.sh ...` dapat dijalankan lagi untuk update; Compose membangun ulang dan mengganti container tanpa menghapus volume.

Untuk menghentikan service tanpa menghapus data:

```sh
docker compose stop
```

`docker compose down` juga mempertahankan named volumes. **Jangan gunakan `docker compose down -v`, `docker volume prune`, atau `docker system prune --volumes`** kecuali memang berniat menghapus database dan file persisten. Compose menyimpan PostgreSQL di volume `postgres_data` (SQLite menyimpan database di `backend_data`) dan berkas upload di folder lokal `uploads/`.

### Backup dan rahasia email

Buat backup database melalui menu Pengaturan sebelum update penting, dan backup folder `uploads/` secara terpisah. Simpan `.env` dan `.mail-encryption.env` secara aman bersama backup; keduanya berisi rahasia deployment dan hanya boleh diakses administrator.

`MAIL_ENCRYPTION_KEY` mengenkripsi password SMTP tersimpan. Jangan mengganti atau membuat ulang `.mail-encryption.env` setelah password SMTP disimpan: key yang berbeda membuat password lama tidak dapat didekripsi. Pulihkan file key yang sama dari backup saat memulihkan deployment. Jika key hilang, masukkan ulang password SMTP setelah deployment pulih. Skrip mempertahankan file key valid dan tidak mengubah konfigurasi `.env` Anda.

Untuk bersih-bersih tanpa menyentuh data aplikasi, artifact build lokal yang diketahui dapat dihapus:

```sh
rm -rf frontend/.next
rm -f backend/api
```

Dangling image Docker bersifat global. Periksa dulu daftar image dengan `docker image ls --filter dangling=true`, lalu hapus hanya ID yang `docker image inspect <id> --format '{{index .Config.Labels "com.docker.compose.project"}}'` pastikan milik proyek ini. Jangan memakai prune volume. Build cache Docker juga bersifat global; jangan bersihkan seluruh cache pada host bersama tanpa memastikan cache tersebut tidak dipakai proyek lain.

### Email rapat

Administrator dapat mengatur SMTP, email pengirim, alamat Sarana Umum, alamat admin untuk peringatan kegagalan, jenis email, dan template melalui **Pengaturan → Email & notifikasi**. SMTP wajib menggunakan TLS (implicit TLS atau STARTTLS). Kata sandi SMTP dienkripsi memakai `MAIL_ENCRYPTION_KEY`; jangan mengubah key setelah password SMTP disimpan kecuali password dimasukkan ulang. Jalankan uji SMTP sebelum mengaktifkan alur email produksi.

Worker di proses API memeriksa pengingat rapat/evidence dan mengirim batch outbox setiap menit; tidak diperlukan endpoint dispatch terpisah. Email yang gagal tetap tercatat di tabel outbox; administrator dapat mencoba ulang sampai 100 pesan gagal dari Pengaturan. Jika pengiriman mencapai batas retry, portal mengantrekan pemberitahuan ke alamat admin yang dikonfigurasi. Status Terkirim hanya berarti server SMTP menerima pesan, bukan bukti masuk ke kotak surat. Email pengajuan baru diteruskan ke approver, undangan resmi dikirim setelah persetujuan, dan konsumsi dikirim ke Sarana Umum hanya jika rapat disetujui serta memiliki permintaan konsumsi.

## Menjalankan lokal

1. Pasang Go 1.27.3 atau lebih baru dan Node.js 22. Jalankan backend dan frontend sebagai proses terpisah; keduanya tetap dapat berjalan/restart sendiri saat service lain tidak tersedia.
2. Di terminal pertama, jalankan `cd backend`, set `DATABASE_URL` ke PostgreSQL (`postgres://...`) atau SQLite (`file:./data/uip.db`), isi `AUTH_SECRET`, `DEMO_PASSWORD`, atur `UPLOAD_DIR=../uploads` dan `PUBLIC_APP_URL`, lalu jalankan `go run ./cmd/api`. Seed data dengan `go run ./cmd/api seed`.
3. Di terminal kedua, jalankan `cd frontend && npm ci && npm run dev`. Frontend meneruskan `/api` ke Go pada `http://localhost:8080` (ubah `GO_API_URL` bila perlu).
4. Buka http://localhost:3000.

Skema database diterapkan otomatis oleh Go saat startup. Perintah seed Go: `cd backend && go run ./cmd/api seed`.

## Test dan validasi

- `cd frontend && npm run build` membangun aplikasi dan memeriksa TypeScript.
- `cd backend && go test -race ./... && go vet ./...` menguji service Go dan perilaku SQLite.
- `cd frontend && npm run test:unit` menguji validasi rentang rapat dan interval jadwal.
- Setelah Go API dan seed aktif, `cd frontend && npm run test:api` menguji login, otorisasi, konflik jadwal dan race condition, upload/download, workflow, ekspor, dan logout. Test API membuat beberapa rapat bertajuk `Smoke test` dalam status cancelled/rejected agar riwayat audit dapat dipertahankan.
- `GET /api/health` melaporkan status aplikasi dan koneksi database.
- `GET /healthz` memeriksa frontend secara lokal tanpa bergantung pada backend. Jika backend tidak tersedia, UI menampilkan banner dan mencoba kembali otomatis; jika frontend sendiri mati, pemantauan memerlukan health check eksternal.

## API

Spesifikasi OpenAPI lengkap tersedia di [openapi.yaml](./openapi.yaml). API mencakup auth, rapat, workflow, ruangan, ketersediaan, bidang, pengguna, notifikasi, pengumuman, dokumen, audit, laporan bulanan, dan ekspor. Semua endpoint bisnis membutuhkan cookie session; workflow dan master-data mutations memeriksa role.

## Batasan integrasi

Zoom/Teams belum terhubung ke provider eksternal: aplikasi menyimpan tautan rapat daring, konfigurasi kapasitas resource/breakout room, serta menghitung bentrok lokal. Integrasi email memakai SMTP tetapi kredensial server harus diisi oleh admin. SSO dan object storage belum disediakan. Penyimpanan file lokal sesuai untuk deployment single-host; gunakan storage provider bersama sebelum menjalankan banyak instance.
