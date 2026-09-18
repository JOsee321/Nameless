# PRD: Nameless

**OSINT Correlation Engine — Unified Reconnaissance Framework**

| | |
|---|---|
| **Versi Dokumen** | 1.0 |
| **Status** | Draft — Fase Perencanaan |
| **Bahasa Implementasi** | Go 1.22+ |
| **Pemilik Produk** | Jose |

---

## 1. Ringkasan Eksekutif

**Nameless** adalah framework OSINT terpadu yang menggabungkan empat kapabilitas recon yang sebelumnya terpisah — web crawling (Photon), username enumeration (Sherlock), passive domain/email harvesting (theHarvester), dan email registration checking (Holehe) — ke dalam satu binary Go performa tinggi dengan **correlation layer** yang menghubungkan temuan antar-modul secara otomatis.

Masalah yang diselesaikan: empat tool asli ditulis di Python, berjalan independen, tidak saling berbagi konteks hasil, dan punya bottleneck arsitektural (client HTTP baru per request, tanpa connection pooling, concurrency model yang mahal). Nameless menyatukan keempatnya dengan HTTP client bersama, worker pool goroutine, dan graph hasil yang saling terhubung — sehingga satu domain target bisa otomatis memicu pencarian username, email, dan subdomain terkait tanpa campur tangan manual di antara tiap tahap.

---

## 2. Latar Belakang & Motivasi

### 2.1 Masalah dengan Tools Existing

| Tool Asli | Bahasa | Masalah Arsitektural |
|---|---|---|
| Photon | Python | Regex re-compile per match, tidak ada connection reuse, crawl single-context |
| Sherlock | Python (asyncio) | Overhead event loop, tidak ada shared client antar-module lain |
| theHarvester | Python | Query source pihak ketiga sequential per source, tidak paralel penuh |
| Holehe | Python | Pola request per-platform mirip Sherlock, tanpa rate limiting adaptif |

### 2.2 Kesenjangan Fungsional

Tidak ada satu pun dari keempat tool ini yang melakukan **korelasi otomatis** — misalnya, email yang ditemukan Photon saat crawling tidak otomatis dicek keberadaannya di platform lain lewat Holehe, dan local-part dari email tersebut tidak otomatis dijadikan kandidat username untuk dicek Sherlock. Analis harus menjalankan tiap tool secara manual dan menghubungkan hasil sendiri.

### 2.3 Peluang

Keempat tool bersifat **I/O-bound**, bukan CPU-bound — karakteristik yang membuat migrasi ke Go dengan goroutine + worker pool berpotensi memberi peningkatan throughput yang signifikan dibanding threading/asyncio Python, sekaligus menyederhanakan deployment (single static binary, tanpa dependency Python runtime).

---

## 3. Tujuan & Sasaran

### 3.1 Tujuan Produk

1. Menyatukan 4 kapabilitas recon ke dalam satu CLI tool yang koheren.
2. Membangun correlation layer yang menghubungkan entitas temuan (domain → email → username → platform) secara graph, bukan output terpisah per-tool.
3. Meningkatkan performa eksekusi secara terukur dibanding menjalankan 4 tool asli secara terpisah, melalui shared HTTP client, worker pool, dan rate limiting per-domain.
4. Menjadikan daftar situs/platform sebagai data eksternal (JSON/YAML), bukan hardcoded, agar mudah di-maintain dan diperluas komunitas.

### 3.2 Sasaran Terukur (Success Metrics)

| Metrik | Target |
|---|---|
| Waktu eksekusi full-scan (crawl + username + harvester + emailcheck) vs 4 tool asli dijalankan berurutan | Lebih cepat, diukur via benchmark suite internal |
| Jumlah platform didukung modul username | ≥ setara Sherlock (400+) di rilis stabil |
| Jumlah platform didukung modul emailcheck | ≥ setara Holehe di rilis stabil |
| False positive rate modul username/emailcheck | ≤ tool asli (regresi tidak boleh lebih buruk) |
| Memory footprint per scan | Terukur dan stabil di bawah beban concurrency tinggi (tidak ada goroutine leak) |

### 3.3 Non-Tujuan (Out of Scope untuk v1)

- Tidak membangun UI web/dashboard di v1 — fokus CLI dan output file (JSON/CSV/HTML report statis).
- Tidak melakukan active exploitation atau interaksi intrusif ke target (tetap murni passive/OSINT).
- Tidak membangun distributed scanning (multi-node) di v1 — single-machine concurrency dulu.
- Tidak menyertakan API key management UI — konfigurasi API key via file config saja.

---

## 4. Target Pengguna

- **Peneliti keamanan / bug bounty hunter** yang butuh recon cepat untuk scoping target.
- **Red teamer** untuk footprinting awal sebelum engagement.
- **Digital investigator / OSINT analyst** yang butuh korelasi identitas lintas platform.
- Pengguna adalah operator teknis yang nyaman dengan CLI — tidak perlu GUI di v1.

**Prasyarat penggunaan**: tool ini ditujukan untuk riset yang sah (misalnya dalam lingkup Vulnerability Disclosure Program, penilaian keamanan terotorisasi, atau investigasi terhadap data yang memang publik). Bukan untuk penyalahgunaan terhadap individu tanpa dasar yang sah.

---

## 5. Arsitektur Sistem

### 5.1 Struktur Direktori

```
osint-fusion/                     # nama repo, binary bernama "nameless"
├── cmd/
│   └── nameless/main.go          # CLI entrypoint
├── internal/
│   ├── core/
│   │   ├── httpclient.go         # shared client: keep-alive, HTTP/2, proxy rotation
│   │   ├── ratelimiter.go        # per-domain token bucket
│   │   ├── worker.go             # generic goroutine worker pool
│   │   └── aggregator.go         # unified result schema + dedup
│   ├── modules/
│   │   ├── crawler/              # eks-Photon
│   │   ├── username/             # eks-Sherlock
│   │   ├── harvester/            # eks-theHarvester
│   │   └── emailcheck/           # eks-Holehe
│   ├── correlator/
│   │   └── correlator.go         # cross-reference antar-entitas
│   └── output/
│       ├── json.go
│       ├── csv.go
│       └── html_report.go
├── data/
│   ├── sites_username.json
│   └── sites_emailcheck.json
├── configs/
│   └── default.yaml
└── go.mod
```

### 5.2 Komponen Inti

#### 5.2.1 Shared HTTP Client (`core/httpclient.go`)
Satu instance `http.Client` dipakai oleh semua modul — bukan instance baru per-request seperti pada implementasi asli. Konfigurasi:
- `MaxIdleConnsPerHost` tinggi untuk connection reuse.
- HTTP/2 diaktifkan bila didukung target.
- Timeout per-request configurable, dengan context cancellation.
- Dukungan proxy rotation (opsional, untuk menghindari rate limiting agresif).

#### 5.2.2 Worker Pool (`core/worker.go`)
Pool goroutine generik berbasis channel + semaphore. Concurrency level configurable via flag (`--concurrency`). Setiap modul mengirim job ke pool yang sama, sehingga total beban concurrent ke jaringan terkontrol secara global, bukan per-modul secara independen.

#### 5.2.3 Rate Limiter (`core/ratelimiter.go`)
Token bucket **per-domain** (menggunakan `golang.org/x/time/rate`), bukan global — supaya scan tidak diblokir kolektif namun tetap agresif ke domain yang toleran terhadap traffic tinggi.

#### 5.2.4 Aggregator (`core/aggregator.go`)
Struktur data hasil terpadu (lihat §5.4) dengan deduplikasi entitas (misalnya email yang sama ditemukan dari crawler dan harvester tidak dicatat dua kali).

### 5.3 Modul Fungsional

| Modul | Fungsi | Sumber Referensi | Input | Output |
|---|---|---|---|---|
| `crawler` | Crawl domain, ekstrak link, JS file, email, endpoint, potensi secret/API key | Photon | Domain/URL target | Daftar URL, email, JS asset, endpoint, temuan secret |
| `username` | Enumerasi keberadaan username di 400+ platform | Sherlock | Username | Daftar platform tempat username ditemukan aktif |
| `harvester` | Passive discovery subdomain & email via crt.sh, DNS, search engine | theHarvester | Domain | Daftar subdomain, email terkait domain |
| `emailcheck` | Cek apakah email terdaftar di suatu platform (via endpoint reset-password) | Holehe | Email | Daftar platform tempat email terdaftar |

Setiap modul mengimplementasikan interface umum (`Module` interface) agar dapat dipanggil seragam oleh orchestrator dan correlator.

### 5.4 Skema Data Terpadu

Entitas hasil direpresentasikan sebagai graph sederhana:

```
Entity {
  ID          string
  Type        enum(Domain, Subdomain, Email, Username, Platform, Secret, Endpoint)
  Value       string
  SourceModule string   // modul yang menemukan entitas ini
  RelatedTo   []EntityID // relasi ke entitas lain
  Metadata    map[string]string
  Timestamp   time.Time
}
```

Semua modul menulis ke struktur ini, bukan output independen — inilah dasar dari correlation layer.

### 5.5 Correlation Layer

Alur korelasi otomatis (dapat dinonaktifkan via flag `--no-correlate` untuk mode single-module):

1. `crawler` menemukan email saat crawling domain → email otomatis masuk antrian `emailcheck`.
2. `crawler`/`harvester` menemukan subdomain baru → opsional re-crawl otomatis (dengan depth limit untuk mencegah infinite loop).
3. Local-part dari email (`john.doe@x.com` → `john.doe`, `johndoe`) di-generate sebagai kandidat username → masuk antrian `username`.
4. Semua entitas temuan disatukan dalam satu graph akhir, diekspor sebagai laporan yang menunjukkan relasi domain → email → username → platform aktif.

### 5.6 Data-Driven Site Definitions

Daftar situs untuk modul `username` dan `emailcheck` disimpan sebagai file JSON eksternal (`data/sites_username.json`, `data/sites_emailcheck.json`), berisi:
- Nama situs
- URL pattern (dengan placeholder username/email)
- Metode deteksi (status code, regex pada response body, atau kombinasi)
- Rate limit khusus situs (opsional override dari default)

Menambah situs baru = edit JSON, tidak perlu rebuild binary (file dimuat saat runtime dari path yang dikonfigurasi).

---

## 6. Spesifikasi Fungsional Detail

### 6.1 CLI Interface

```
nameless scan --target <domain|username|email> [flags]

Flags:
  --mode string        full | crawl | username | harvester | emailcheck (default "full")
  --concurrency int    jumlah worker paralel (default 50)
  --timeout duration   timeout per-request (default 10s)
  --rate-limit int     request per detik per-domain (default 5)
  --proxy string       proxy list file (opsional)
  --output string      path output (default stdout)
  --format string      json | csv | html (default "json")
  --no-correlate       nonaktifkan correlation layer, jalankan modul secara independen
  --config string      path ke file konfigurasi custom (default configs/default.yaml)
  --depth int           kedalaman crawl untuk modul crawler (default 2)
```

### 6.2 Mode Operasi

- **`full`**: menjalankan seluruh pipeline (crawl → harvester → correlation → username/emailcheck otomatis dari temuan).
- **`crawl`**, **`username`**, **`harvester`**, **`emailcheck`**: menjalankan satu modul saja, untuk kasus ketika pengguna sudah punya input spesifik (misalnya langsung punya daftar username untuk dicek).

### 6.3 Format Output

- **JSON**: struktur graph lengkap (entities + relations), untuk konsumsi tool lain.
- **CSV**: flat table per tipe entitas, untuk import ke spreadsheet.
- **HTML report**: laporan visual statis (tanpa dependency eksternal saat dibuka offline) menampilkan relasi domain → email → username → platform.

---

## 7. Rencana Implementasi (Fase & Commit Discipline)

Setiap fase diakhiri commit git dengan pesan deskriptif dan (jika relevan) tag versi.

| Fase | Cakupan | Deliverable |
|---|---|---|
| **Fase 0** | Scaffold repo, CLI skeleton (cobra), config loader (YAML), shared HTTP client, rate limiter dasar | Binary jalan, `nameless --help` berfungsi |
| **Fase 1** | Port Sherlock → modul `username`. Data-driven JSON site list. Worker pool terintegrasi. Benchmark vs Sherlock asli | Modul `username` fungsional + laporan benchmark |
| **Fase 2** | Port Holehe → modul `emailcheck`. Reuse worker pool & HTTP client dari Fase 1 | Modul `emailcheck` fungsional |
| **Fase 3** | Port Photon → modul `crawler` (extract link, JS, email regex, secret pattern, form) | Modul `crawler` fungsional |
| **Fase 4** | Port theHarvester → modul `harvester` (crt.sh, DNS enumeration minimal, sumber pasif lain) | Modul `harvester` fungsional |
| **Fase 5** | Bangun `correlator` + skema graph terpadu + output JSON/CSV/HTML | Correlation layer aktif, laporan relasi entitas |
| **Fase 6** | Tuning performa: proxy rotation, adaptive rate limiting, benchmark suite komparatif (Nameless vs 4 tool asli dijalankan terpisah) | Laporan performa akhir, dokumentasi tuning |
| **Fase 7** | Hardening: unit test coverage modul inti, dokumentasi penggunaan, README, contoh konfigurasi | Rilis kandidat v1.0 |

---

## 8. Tech Stack

| Kebutuhan | Pilihan |
|---|---|
| Bahasa | Go 1.22+ |
| CLI framework | `cobra` |
| HTML parsing (pengganti BeautifulSoup di Photon) | `goquery` |
| Rate limiting | `golang.org/x/time/rate` |
| DNS resolver custom (untuk harvester) | `github.com/miekg/dns` |
| Konfigurasi | YAML (`gopkg.in/yaml.v3`) |
| Definisi situs | JSON, dimuat runtime |

---

## 9. Pertimbangan Performa

1. **Connection reuse**: satu `http.Client` lintas modul menghilangkan overhead pembuatan koneksi TLS berulang yang terjadi di tiap tool Python asli.
2. **Worker pool tunggal**: mencegah oversubscription jaringan ketika beberapa modul berjalan bersamaan dalam mode `full`.
3. **Regex pre-compiled** saat inisialisasi, bukan per-match seperti Photon asli.
4. **Streaming output**: hasil ditulis begitu satu entitas selesai diproses, bukan menunggu seluruh pipeline selesai — penting untuk scan berdurasi lama.
5. **Rate limiting adaptif per-domain**: mencegah satu domain yang lambat/restriktif memperlambat keseluruhan scan (domain lain tetap diproses paralel).
6. **Benchmark wajib** di setiap fase yang menyentuh modul porting — dibandingkan langsung dengan tool asli pada target yang sama, dicatat di dokumentasi fase.

---

## 10. Risiko & Mitigasi

| Risiko | Dampak | Mitigasi |
|---|---|---|
| Situs target mengubah struktur HTML/endpoint, deteksi Sherlock/Holehe jadi usang | Data source jadi stale, false negative | Site definitions eksternal (JSON) memudahkan update tanpa rebuild; pertimbangkan mekanisme community update |
| Rate limiting / blocking dari platform pihak ketiga akibat concurrency tinggi | Scan gagal sebagian, IP diblokir | Rate limiter per-domain, dukungan proxy rotation, exponential backoff |
| False positive pada deteksi username/email registration | Kualitas intelijen menurun | Uji regresi terhadap dataset dari tool asli, dokumentasikan metode deteksi tiap situs |
| Correlation layer menghasilkan noise (kandidat username dari email yang tidak relevan) | Laporan kurang presisi | Beri skor confidence per relasi, tandai relasi hasil-generate vs hasil-observasi langsung |
| Scope creep menuju active scanning/exploitation | Melanggar prinsip OSINT pasif | Non-tujuan didefinisikan eksplisit di §3.3; tidak ada fitur exploitation di roadmap |

---

## 11. Etika Penggunaan

Nameless dibangun untuk riset keamanan yang sah — pengujian dalam lingkup VDP/bug bounty yang berwenang, asesmen keamanan terotorisasi, dan investigasi berbasis data publik. Tool ini tidak menyertakan mekanisme bypass otorisasi atau eksploitasi aktif. Tanggung jawab kepatuhan hukum dan etika penggunaan berada di tangan operator.

---

## 12. Lampiran: Referensi Tool Asal

| Tool | Repository |
|---|---|
| Photon | github.com/s0md3v/Photon |
| Sherlock | github.com/sherlock-project/sherlock |
| theHarvester | github.com/laramies/theHarvester |
| Holehe | github.com/megadose/holehe |