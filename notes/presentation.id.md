# Flash Cashback — Presentasi Proyek

Versi Bahasa Indonesia dari [presentation.md](./presentation.md) — catatan persiapan untuk menjelaskan proyek ini secara lisan, disusun sebagai **Konteks → Pendekatan → Trade-off → Bukti → Catatan/langkah selanjutnya**, yang lebih cocok untuk membahas satu proyek secara utuh dibanding metode STAR (pemisahan Situation/Task pada STAR jadi kabur di luar konteks satu insiden, dan tidak punya tempat alami untuk membahas trade-off). Satu-satunya bagian yang memang cocok dengan STAR — pertanyaan "ceritakan tentang bug yang pernah ditemui" — sengaja diberi label seperti itu, di bagian Tanya-Jawab. Detail teknis lengkap ada di `spec/` (`requirements.md`, `design.md`, `decisions.md`, `invariants.md`, `risks.md`, `api.yaml`); dokumen ini adalah versi "dijelaskan secara lisan".

Status: backend dan aplikasi mobile sudah diimplementasikan dan diverifikasi — bukan mockup, angka-angka pada demo adalah hasil nyata.

---

## 1. Latar belakang — apa yang sebenarnya diminta

Ringkasan brief: pengguna mendapatkan cashback 5% dari pembayaran di atas 20.000 IDR, dibatasi 50.000 IDR per pengguna per hari, dan total budget kampanye 10.000.000 IDR, serta bisa menukarkan (redeem) saldo mereka. Go, Postgres, Redis, React Native. Dibangun dengan bantuan AI, tapi tetap "MVP, kelas produksi" — cakupan kecil, tapi semuanya dikirim dalam kondisi siap produksi.

Brief-nya jelas untuk jalur normal (happy path), tapi diam di beberapa titik penting. Tidak ada yang menyebutkan apa yang terjadi kalau dua pembayaran untuk pengguna yang sama datang di milidetik yang sama. Tidak disebutkan berapa jumlah minimum penukaran (redemption), apakah kampanye punya batas waktu, atau reason code mana yang menang kalau satu pembayaran menyentuh batas harian dan budget kampanye pada saat yang persis bersamaan. Semua itu bukan kelalaian. Sebuah layanan yang "memindahkan uang sungguhan," dinilai dari kesiapan produksinya, yang diam-diam menebak celah-celah itu atau tidak menangani penulisan konkuren dengan benar bukanlah versi lebih kecil dari sistem yang benar — itu adalah sistem yang salah, yang kebetulan berjalan mulus di demo single-thread.

Jadi cakupan sebenarnya dari proyek ini ada dua hal, berurutan: (1) temukan setiap titik di mana brief diam atau ambigu, buat keputusan nyata dengan alasan nyata alih-alih menebak diam-diam, lalu tuliskan; (2) buktikan — bukan sekadar klaim — bahwa perhitungan uang tetap benar di bawah akses konkuren, karena itulah satu-satunya properti yang tidak bisa dipalsukan begitu saja lewat demo langsung.

---

## 2. Pendekatan — bagaimana dari situ sampai ke sistem yang jalan

**Spec sebelum kode.** Setiap titik ambigu diselesaikan lebih dulu di `spec/decisions.md`, sebelum diimplementasikan, masing-masing dengan alasan dan alternatif yang saya tolak — bukan cuma jawabannya saja — delapan keputusan pada akhirnya (jumlah minimum redemption, jendela waktu kampanye, tie-break reason code, batas `paid_at`, penanganan konflik idempotency-key, transport untuk masuknya data pembayaran, visibilitas budget kampanye, identitas pengguna). Kalau saya mengambil keputusan tanpa menuliskan alasannya, itu persis "tebakan diam-diam" yang diuji oleh brief ini.

**Setiap celah yang disebut di bagian Latar Belakang dijawab langsung, tidak dibiarkan menggantung:** masalah konkurensi di milidetik yang sama itulah yang menjadi alasan mekanisme locking di bawah ini ada; jumlah minimum redemption, jendela waktu kampanye, dan tie-break reason code masing-masing diselesaikan di bagian Trade-off, lengkap dengan alternatif yang saya tolak dan alasannya. Bagian Bukti kemudian menunjukkan bahwa tiga dari empat hal itu bukan cuma keputusan di atas kertas.

**Arsitektur, stateless sejak awal desain:**

```
Mobile (Expo, RN)  --HTTPS, header X-User-Id-->  Go API (net/http, stateless)
                                                        |
                                                        v
                                              Postgres (sumber kebenaran)
                                              Redis (baru dipakai readyz)
```

Tanpa web framework, tanpa ORM — stdlib `net/http` dengan routing pola-metode Go 1.22+, `pgx/v5`, `go-redis/v9`. API ini tidak menyimpan state antar-request; setiap koordinasi lintas-request (batas harian, budget, idempotency) terjadi di Postgres lewat row lock. Itulah yang membuat "jalankan tiga replika di belakang load balancer" jadi hal yang biasa saja, bukan perlu desain ulang.

**Mekanisme intinya:** setiap pembayaran diproses dalam satu transaksi yang mengunci baris budget kampanye, lalu baris batas-harian milik pengguna, dalam urutan tetap itu (untuk menghindari deadlock), menghitung jumlah yang bisa diberikan terhadap kedua sisa batas tersebut, lalu meng-commit semuanya — award-nya, entri ledger, counter yang ter-update — secara atomik. Idempotency berlaku dua arah: `payment_id` sebuah pembayaran adalah baris klaim yang di-insert dengan `ON CONFLICT DO NOTHING`, jadi duplikat yang datang bersamaan akan kalah race di level database dan cukup membaca kembali hasil dari pemenangnya, bukan meng-award ulang; penukaran (redemption) bekerja dengan cara yang sama, dikunci lewat `(user_id, idempotency_key)`.

**Hasilnya:** seluruh siklus hidup cashback (masuk → award → penegakan batas → redeem) lengkap dengan enam reason code, idempotency yang benar-benar bertahan di bawah konkurensi, seluruh permukaan API (`POST /payments`, `GET /cashback/{balance,daily,history}`, `POST /cashback/redemptions`, `GET /campaign`), dan satu layar mobile yang terhubung ke API sungguhan — saldo, pemakaian hari ini, status kampanye, redeem, riwayat lengkap — yang tidak pernah menghitung cashback sendiri, hanya menampilkan apa kata server (NFR-07, dan ini krusial: klien yang *bisa* menghitung cashback-nya sendiri adalah klien yang tidak bisa dipercaya).

---

## 3. Trade-off — keputusan dengan alternatif nyata yang saya pertimbangkan serius lalu tolak

Empat yang pertama ini menjawab persis celah-celah yang disebut di bagian Latar Belakang, sesuai urutan kemunculannya di sana. Sisanya adalah trade-off lain yang tetap dibutuhkan proyek ini, tapi tidak disebut namanya di bagian Latar Belakang.

### 1. Pembayaran konkuren di milidetik yang sama: satu lock pada baris budget vs. counter lock-free
Pola lock-free `UPDATE ... WHERE remaining >= 0` bisa scale lebih jauh, tapi tidak bisa menghitung award *parsial* dengan benar — Anda butuh jumlah sisa yang persis, bukan cuma tahu apakah masih ada sisa atau tidak — jadi tetap butuh satu pembacaan tambahan, dan sebagian besar keuntungan throughput-nya jadi hilang. Saya pilih lock: sederhana dan jelas benar, mengalahkan skema yang lebih sulit diverifikasi dan tidak banyak untungnya di skala budget ini (10.000.000 IDR — kampanyenya memang berumur pendek).

### 2. Jumlah minimum redemption: 1.000 IDR vs. batas akumulasi yang lebih tinggi
Brief tidak menentukan ini. 1.000 IDR adalah persis award cashback tunggal terkecil yang mungkin terjadi menurut aturan yang ada (5% dari pembayaran minimum-eligible 20.000 IDR) — tetapkan batasnya di situ, dan seorang pengguna tidak akan pernah punya saldo yang tidak bisa ditukarkan setelah hanya satu pembayaran yang memenuhi syarat. Alternatif nyatanya adalah batas yang lebih tinggi, pola yang lebih umum di dunia nyata (memaksa sedikit akumulasi sebelum penukaran pertama) — ditolak karena tidak ada gunanya di sini selain itu, dan batas yang lebih rendah pun tidak berarti apa-apa karena tidak mungkin ada award yang lebih kecil. Diberlakukan sebagai `MinRedemptionIDR` di handler redemption, dipastikan lewat `TestRedeem_BelowMinimumRejected`.

### 3. Jendela waktu kampanye: kondisi akhir berbasis budget saja vs. jendela waktu, atau keduanya dengan aturan siapa-lebih-dulu-berakhir
Brief menyebutkan tepat satu kondisi akhir — kampanye berakhir saat budget 10.000.000 IDR habis — dan tidak menyebut apa pun soal tanggal. Saya sempat mempertimbangkan menambahkan jendela waktu, karena kampanye flash-sale di dunia nyata hampir selalu punya jam dan budget sekaligus, dengan aturan gabungan yang jelas "siapa yang lebih dulu tercapai, itu yang mengakhiri." Saya menolaknya: itu persyaratan yang tidak diminta, `campaign_budget` akan butuh kolom `start_at`/`end_at`, dan `CAMPAIGN_ENDED` harus mengecek dua kondisi alih-alih satu — dua sumber kebenaran yang bisa saling tidak sepakat soal mana yang benar-benar mengakhiri kampanye, dibanding satu pengecekan boolean sederhana hari ini (`awarded_total_idr >= total_budget_idr`). Ini pun tidak menutup pintu: kalau nanti benar-benar dibutuhkan promo berbasis waktu, kolom-kolom itu tinggal ditambahkan ke tabel yang sama tanpa menyentuh ledger atau logika award — bersifat tambahan, bukan penulisan ulang.

### 4. Tie-break reason code saat kedua batas terikat bersamaan: budget yang menang
Saat batas harian dan budget kampanye sama-sama membatasi pembayaran yang sama pada nilai sisa yang persis sama, total yang di-award tetap identik — hanya reason code yang dilaporkan yang berbeda. Dua alternatif dipertimbangkan: mempertahankan default lama `PARTIAL_DAILY_CAP` (membingkainya sebagai "Anda pribadi kena batas," padahal mengecilkan fakta bahwa kampanye itu sendiri juga sedang di ambang batas untuk semua orang), atau menambahkan kode gabungan baru `PARTIAL_DAILY_CAP_AND_BUDGET` (paling akurat secara harfiah, tapi memperluas set tetap enam reason code di FR-09 hanya demi kasus tepi tie-break yang langka, memaksa setiap konsumen — termasuk aplikasi mobile — menangani nilai ke-7). Saya pilih `PARTIAL_BUDGET`: habisnya budget kampanye adalah kejadian global, sekali terjadi, memengaruhi semua pengguna, jadi lebih penting untuk ditonjolkan dibanding batas harian pribadi yang toh akan reset besok. Dipastikan lewat kasus exact-tie di `TestComputeAward`, bukan cuma diputuskan di atas kertas.

### HTTP sinkron vs. message queue untuk masuknya data pembayaran
Payment processor sungguhan mungkin butuh queue untuk jaminan pengiriman saat terjadi outage. Tidak krusial di skala proyek ini — ini reviewer yang menjalankan demo, bukan sistem yang harus bertahan dari trafik payment provider sungguhan — dan ini bukan keputusan yang tidak bisa dibalik: desain idempotency berbasis `payment_id` justru persis yang dibutuhkan konsumer queue juga, jadi logika award-nya sendiri tidak perlu berubah kalau ini dipindah ke asynchronous nanti.

### Counter yang dimaterialisasi vs. menghitung ulang dari ledger setiap kali dibaca
Saldo, total harian, dan budget yang terpakai adalah counter berjalan yang dijaga tetap sinkron dengan ledger append-only lewat disiplin transaksi, bukan di-`SUM()` setiap kali dibaca. Murah di jalur baca yang sering diakses; biayanya adalah counter ini secara prinsip bisa drift kalau suatu saat ada perubahan yang melanggar disiplin itu, dan tidak ada yang otomatis menangkapnya hari ini. Diterima sebagai risiko yang didokumentasikan, alih-alih membangun job rekonsiliasi yang belum dibutuhkan apa pun di cakupan proyek ini saat ini.

### Endpoint identitas `POST /users` vs. skrip SQL seed
Saya membangun versi API-nya lebih dulu — find-or-create pengguna berdasarkan nama, pola race-safe yang sama seperti pembayaran. Lalu saya cabut kembali: pembuatan pengguna bukan sesuatu yang diminta brief dari layanan ini ("anggap pengguna sudah dikenal"), dan endpoint yang bisa membuat identitas baru memperluas permukaan API untuk urusan yang bukan tanggung jawab layanan ini. `users` sekarang di-seed lewat skrip SQL biasa dengan id demo yang tetap. UX berdiri sendiri jadi lebih buruk (tempel UUID, bukan ketik nama), sebagai ganti kejujuran soal apa sebenarnya sistem ini.

### Redis ada di stack vs. Redis benar-benar melakukan sesuatu
Redis diwajibkan oleh brief dan sudah terpasang (`/readyz` bergantung padanya), tapi hari ini dia tidak melakukan apa-apa selain health check itu — setiap baca dan tulis langsung ke Postgres. Saya sempat mempertimbangkan membangun dua fungsi jelasnya sekarang: cache TTL pendek untuk `GET /campaign`/`GET /cashback/balance`, dan rate limiter token-bucket pada `POST /payments`/`POST /cashback/redemptions`. Saya tidak melakukannya, dengan alasan yang sama dua kali: tidak ada trafik sungguhan di demo untuk memvalidasi keduanya, dan seluruh kredibilitas proyek ini bertumpu pada klaim yang dibuktikan lewat concurrency test, bukan "seharusnya berhasil" — cache atau limiter yang tidak bisa saya load-test persis jenis kode belum-terverifikasi yang sejak awal ditolak proyek ini. Membiarkan Redis menganggur tidak merugikan sisi kebenaran data (NFR-02: dia memang tidak pernah diizinkan jadi otoritas untuk uang) dan menjaga disiplin itu tetap konsisten; trade-off-nya adalah optimisasi nyata yang belum diambil, tapi sudah diperhitungkan dan siap dibangun (cache TTL-only dulu — dia tidak bisa "berbohong" lama-lama — invalidate-on-write hanya kalau staleness benar-benar jadi masalah).

---

## 4. Bukti — bagaimana saya tahu ini benar-benar berlaku, bukan sekadar saya desain begitu

Niat desain dan kode yang berjalan adalah dua klaim yang berbeda, dan celah di antara keduanya persis tempat sistem "concurrent-safe" biasanya gagal diam-diam. Karena itu `spec/invariants.md` menuliskan properti kebenaran sebagai klaim formal — *"kampanye tidak pernah meng-award lebih dari budget-nya, selalu, termasuk pada momen dua pembayaran konkuren sama-sama mencoba menghabiskan sisa budget terakhir"* — dan masing-masing punya test yang mencoba melanggarnya di bawah beban konkuren sungguhan terhadap Postgres sungguhan, bukan mock.

Dua angka konkret: 20 goroutine berjalan bersamaan, masing-masing mengirim pembayaran senilai cashback 5.000 IDR terhadap batas harian 50.000 IDR milik satu pengguna (100.000 diminta, 50.000 tersedia) — test-nya memastikan total akhirnya *persis* 50.000, bukan "mendekati." 300 goroutine berjalan bersamaan lintas 300 pengguna berbeda, masing-masing meminta cashback 50.000 IDR terhadap budget kampanye 10.000.000 IDR (15.000.000 diminta) — assertion yang sama, total akhir yang persis, tidak ada kelebihan award.

Tiga celah lain yang disebut di bagian Latar Belakang juga bukan sekadar keputusan tertulis — dipastikan dengan cara yang sama. `TestRedeem_BelowMinimumRejected` membuktikan batas 1.000 IDR benar-benar ditegakkan, bukan cuma didokumentasikan. Kasus exact-tie di `TestComputeAward` membuktikan tie-break reason code memang mengarah ke `PARTIAL_BUDGET`, bukan sekadar kebetulan hasil fallback kode. Jendela waktu kampanye adalah satu-satunya pengecualian, dan memang disengaja: tidak ada invariant untuk diuji karena memang tidak ada yang dihitung — keputusannya adalah tidak menambahkan logika tanggal sama sekali, jadi buktinya bersifat struktural (tidak ada kolom `start_at`/`end_at` yang bisa salah), bukan hasil sebuah test.

Total sekitar 30 automated test: unit test murni untuk logika perhitungan award, dan integration test yang menguji konkurensi sungguhan, semuanya bersih di bawah `-race` — bisa direproduksi dengan `make test-integration`, yang menjalankannya terhadap database sekali-pakai supaya tidak pernah menyentuh data demo yang sudah di-seed.

---

## 5. Catatan penting & langkah selanjutnya

Hal-hal yang akan saya sampaikan sebelum ditanya, berikut apa yang sebenarnya akan saya lakukan untuk masing-masing kalau proyek ini harus melangkah lebih jauh dari sekadar demo:

- **Tidak ada autentikasi.** `X-User-Id` adalah header polos yang tidak diverifikasi. Ini memang yang diminta brief secara eksplisit, tapi ini satu-satunya hal yang saya sebut sebagai hard blocker, bukan sekadar nice-to-have, sebelum sistem ini menyentuh uang sungguhan — autentikasi sungguhan akan jadi langkah pertama di fase berikutnya.
- **`paid_at` bisa di-backdate.** Timestamp di masa depan ditolak (pembayaran tidak bisa berhasil sebelum kejadian yang melaporkannya ada), tapi masa lalu sengaja tidak dibatasi, karena kebutuhan backfill yang sah butuh keleluasaan itu. Langkah berikutnya kalau ini jadi masalah: batasi siapa saja yang boleh memanggil `POST /payments` di level jaringan, karena itulah perlindungan sesungguhnya, bukan aturan timestamp yang lebih ketat.
- **Counter drift mungkin terjadi secara teori, tidak terdeteksi dalam praktik.** Tidak ada job otomatis yang merekonsiliasi counter yang dimaterialisasi terhadap `SUM(ledger)`. Saya tahu persis seperti apa bentuk job itu; membangunnya belum dibenarkan oleh apa pun yang dibutuhkan cakupan proyek ini saat ini.
- **Baris budget kampanye adalah satu titik bottleneck penulisan global.** Tidak masalah di skala budget sekarang. Di throughput sungguhan: sharding counter-nya, atau kembali ke pola lock-free yang saya tolak sebelumnya, begitu ada data beban nyata yang membenarkan kompleksitas tersebut.
- **Redis menganggur** selain untuk health check — lihat "Redis ada di stack vs. Redis benar-benar melakukan sesuatu" di bagian Trade-off di atas untuk alasan dan apa yang dibutuhkan untuk mengubahnya.
- **Belum ada deteksi fraud/abuse.** Mitigasinya sepenuhnya "endpoint ini seharusnya tidak bisa dijangkau pengguna akhir," sebuah urusan deployment/jaringan, bukan sesuatu yang ditegakkan kode saat ini.
- **Aplikasi mobile hanya satu layar**, tujuannya membuktikan API bekerja end-to-end dari klien sungguhan, bukan untuk memamerkan desain produk.

---

## Pertanyaan yang saya duga akan muncul, dan bagaimana saya akan menjawabnya

**"Bagaimana Anda benar-benar menjamin batas harian dan budget tidak pernah terlampaui di bawah beban konkuren?"**
Row-level lock pada dua counter bersama, di dalam satu transaksi per pembayaran, dengan urutan lock yang tetap untuk menghindari deadlock. Dibuktikan lewat test yang menembakkan 300 goroutine konkuren yang meminta 1,5x budget, lalu memastikan total akhirnya persis sama dengan budget.

**"Apa yang terjadi kalau payment_id yang sama datang dua kali pada saat bersamaan?"**
Postgres yang menyerialisasikannya untuk saya: insert baris klaim pada `payment_id` memakai `ON CONFLICT DO NOTHING`, dan insert konkuren pada key yang sama akan saling memblokir di level database. Yang kalah akan melihat 0 baris kembali, roll back, lalu membaca hasil yang sudah di-commit oleh pemenangnya, alih-alih memproses ulang.

**"Kenapa Postgres jadi sumber kebenaran, bukan Redis, padahal Redis sudah ada di stack?"**
Ini bukan soal atomicity — Redis punya `MULTI`/`EXEC` dan Lua script, jadi operasi atomik bukan celahnya. Ini soal durability dan recovery: sebuah award harus tetap bertahan setelah crash dan bisa direkonstruksi kembali dari catatan append-only, dan model persistence Redis tidak memberi saya jaminan itu sebagaimana WAL dan tabel `ledger` milik Postgres. Redis ada di sana untuk optimisasi baca di masa depan, tidak pernah untuk kebenaran data — itu dinyatakan eksplisit di spec saya sendiri (NFR-02).

**"Apa yang akan Anda ubah untuk deployment produksi sungguhan?"**
Berurutan: autentikasi sungguhan, itu dulu, tanpa kompromi. Lalu job rekonsiliasi untuk counter-counternya. Lalu kemungkinan besar queue untuk masuknya data pembayaran, begitu ada payment provider sungguhan dengan kebutuhan jaminan pengiriman yang nyata di sisi lain. Logika inti award/redeem tidak perlu berubah untuk semua ini — memang begitu desainnya.

**"Ceritakan tentang bug yang pernah Anda temui." (STAR)**
*Situation:* sebuah concurrency test menunjukkan saldo redemption yang terlihat salah setelah 10 request identik yang berjalan bersamaan. *Task:* memastikan apakah logika redemption itu sendiri yang benar-benar rusak, sebelum saya buru-buru memperbaiki apa pun. *Action:* alih-alih langsung percaya bahwa bug ada di tempat yang ditunjuk test, saya menarik langsung baris-baris mentah `redemptions` dan `ledger` dari Postgres. *Result:* logika idempotency redemption ternyata benar sejak awal — bug-nya ada di helper setup milik *test itu sendiri*, yang men-seed saldo awal lewat pembayaran yang cukup besar sehingga diam-diam kena batas harian, jadi asumsi saldo awal test itu sendiri yang salah. Saya lebih memilih menceritakan ini apa adanya daripada berpura-pura tidak ada yang meleset; ini contoh yang cukup baik untuk tidak langsung percaya pada penjelasan pertama dari sebuah test yang gagal.

**"Kenapa Anda menulis dokumen spec sebelum menulis kode sama sekali?"**
Karena permintaan sesungguhnya dari brief ini adalah penilaian (judgment), bukan kecepatan mengetik. Setiap titik ambigu mendapat keputusan tertulis lengkap dengan alasan dan alternatif yang ditolak, bukan tebakan diam-diam yang langsung tertanam di kode — delapan keputusan pada akhirnya, dan tidak ada yang dikirim tanpa satu pun keputusan tertulis.

**"Bagaimana ini akan berkembang untuk mendukung multi-currency?"**
Setiap tabel yang menyimpan uang (`payments`, `ledger`, `campaign_budget`, `user_daily_cashback`, `user_balance`, `redemptions`) akan butuh kolom `currency` di samping jumlah integer-nya — saat ini "10.000.000" secara implisit berarti IDR, dan itu berhenti aman begitu ada mata uang kedua. Persimpangan desain yang sesungguhnya adalah apakah kampanye tetap satu kolam budget tunggal (pembayaran mata uang asing dikonversi saat waktu award, memakai kurs yang diambil sekali lalu dibekukan ke entri ledger tersebut — tidak pernah dihitung ulang, disiplin yang sama seperti bagaimana idempotency `payment_id` sudah mengunci hasil sejak insert pertama) atau menjadi satu budget per mata uang, yang locking-nya lebih sederhana tapi mengubah arti "satu kampanye" itu sendiri. Konversi ke satu mata uang dasar adalah jawaban yang lebih realistis, tapi memperkenalkan mode kegagalan yang saat ini sama sekali tidak ada — kurs yang basi atau salah — dan ini satu-satunya tempat di mana Redis akhirnya benar-benar berguna, meng-cache kurs tanpa menyentuh jalur kebenaran transaksi award.

**"Kalau ada 2 kampanye berjalan sekaligus, kampanye mana yang dipotong lebih dulu?"**
Saat ini belum ada konsep multi-kampanye sama sekali — `campaign_budget` adalah baris tunggal (singleton) yang hardcoded, jadi pertanyaan ini belum terjawab oleh skema yang ada sekarang, bukan sekadar belum ditangani saat runtime. Pendekatan saya: beri kampanye sebuah `priority` eksplisit, alih-alih aturan implisit seperti "budget terbesar" atau "tanggal berakhir paling awal" — kedengarannya masuk akal sampai dua kampanye seri, sementara priority yang diatur admin bersifat deterministik dan bisa diaudit, alasan yang sama seperti yang sudah dipakai untuk tie-break batas-harian/budget (pilih satu aturan, tuliskan alasannya, jangan mengarang heuristik yang kabur). Secara konkret: tabel `campaigns`, FK `campaign_id` ditambahkan ke `payments` dan `ledger` supaya ledger append-only tetap bisa menyebutkan kampanye mana yang membayar setiap award, dan saat waktu award, hanya kunci baris kampanye aktif berprioritas tertinggi yang masih punya sisa budget — bentuk transaksinya sama seperti sekarang, hanya saja terhadap baris yang dipilih, bukan baris yang tetap. Trade-off yang akan saya tanam untuk v1: satu kampanye per pembayaran, tidak ada pemecahan award parsial ke dua kampanye kalau kampanye pertama habis di tengah pembayaran — pemecahan itu menyisakan lebih sedikit cashback yang tidak terklaim, tapi mengubah satu reason code dan satu entri ledger per pembayaran menjadi rantai dengan panjang yang bervariasi, jenis kompleksitas yang sama yang sudah ditolak proyek ini sekali, saat memilih satu pemenang tie-break dibanding reason code gabungan.

**"Bagaimana ini akan scale ke trafik sungguhan?"**
Desain single-lock membatasi throughput award secara global — itu batas atas yang sudah diketahui, dan saya sampaikan sendiri di atas alih-alih menunggu ditanya. Perbaikannya adalah sharding counter-nya atau pola lock-free yang sudah saya evaluasi dan simpan dulu, ditinjau ulang begitu ada data beban sungguhan. Pembacaan (reads) sudah bisa scale horizontal karena API-nya stateless; bagian itu bukan bottleneck-nya.

**"Apa satu hal yang akan Anda tunjukkan sebagai bukti ini 'kelas produksi,' bukan sekadar demo?"**
Dokumen invariants dan test-test yang menegakkannya. Bukan "menurut saya ini benar" — melainkan "saya bisa tunjukkan ini benar, termasuk dua angka di titik yang paling penting."
