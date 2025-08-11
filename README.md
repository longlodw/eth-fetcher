# Ethereum Fetcher

A Go server for **fetching Ethereum mainnet block data** (block number, timestamp, gas used, and miner tips) in a user‑specified range using the **Alchemy API**.

- Fetches data in **parallel batches**.
- Writes CSV incrementally to keep memory usage constant.
- Persists results and allows **asynchronous job control**.
- Serves a web dashboard UI from a local folder (`/var/eth-fetcher/frontend`).
- Uses a local SQLite DB for caching results.

---

## ✨ Features

- **Parallel batch fetching** with internal rate limiting in `Analyzer`.
- **Incremental CSV writes** → handles millions of blocks without ballooning RAM.
- Persistent CSV storage under `/var/eth-fetcher/jobs`.
- **SQLite caching** at `/var/eth-fetcher/results.db` to avoid refetching.
- Stop jobs mid‑way → partial contiguous CSV still downloadable.
- Progress tracking via `lastWritten` block.
- Basic web dashboard included and served from the same server.

---

## 📂 Directory Structure

Your server expects:

```
/var/eth-fetcher/
    jobs/        # Generated CSV files
    results.db   # SQLite cache
    frontend/    # Contains dashboard.html and optional static assets
```

---

## 🛠 Requirements

- **Go** 1.20+
- An [Alchemy API key](https://www.alchemy.com/)
- Writable `/var/eth-fetcher` directory

---

## 🔧 Installation & Run (Local)

```
# Clone repo
git clone https://github.com/yourusername/eth-fetcher.git
cd eth-fetcher

# Build binary
go build -o eth-fetcher main.go analyzer.go

# Setup directories
sudo mkdir -p /var/eth-fetcher/jobs /var/eth-fetcher/frontend
sudo chown $(whoami) /var/eth-fetcher/jobs /var/eth-fetcher/frontend

# Place your dashboard.html in /var/eth-fetcher/frontend/
cp dashboard.html /var/eth-fetcher/frontend/

# Set API key
export ALCHEMY_API_KEY=your_alchemy_key_here

# Run
./eth-fetcher
```

Server listens on **`:8080`**.

---

## 🌐 API Endpoints

### `POST /request?start=&end=`
Submit a new job.

Returns:
```
{"jobID": "uuid-here"}
```

---

### `GET /status/{jobID}`
Check job state and progress.

Example:
```
{
  "status": "pending",
  "filePath": "",
  "error": "",
  "start": 18000000,
  "end": 18000100,
  "lastWritten": 18000042
}
```

---

### `GET /stop/{jobID}`
Stops a running job, marks it as `done`, and keeps all contiguous blocks written so far.

---

### `GET /download/{jobID}`
Download the CSV for a completed or stopped job.

---

### `GET /jobs`
Returns a list of all job IDs currently tracked.

---

### `GET /health`
Returns `OK` (for monitoring).

---

### `/` (root)
Serves static files from `/var/eth-fetcher/frontend` (including the dashboard UI).

---

## 📄 CSV Format

Each row is:
```
block_number,timestamp,gas_used,tips
```

- `block_number`: block height
- `timestamp`: block time in Unix format (UTC)
- `gas_used`, `tips`: integer values (wei)

---

## 🖥 Dashboard UI

Place your `dashboard.html` (and any CSS/JS) in `/var/eth-fetcher/frontend`.

Open in browser:
```
http://:8080/dashboard.html
```
or simply:
```
http://:8080/
```
if the dashboard file is named `index.html`.

The dashboard lets you:
- Enter block start/end
- Submit jobs
- View progress
- Stop or download jobs

---

## 🚀 Deploy on Google Compute Engine (Recommended)

1. **Create VM**
   ```
   gcloud compute instances create eth-fetcher \
     --machine-type=e2-medium \
     --image-family=debian-12 \
     --image-project=debian-cloud \
     --boot-disk-size=20GB \
     --tags http-server
   ```

2. **Allow traffic on port 8080**
   ```
   gcloud compute firewall-rules create allow-eth-fetcher \
     --allow tcp:8080 --target-tags=http-server
   ```

3. **Install and run**
   ```
   sudo mkdir -p /var/eth-fetcher/jobs /var/eth-fetcher/frontend
   sudo chown $USER /var/eth-fetcher/jobs /var/eth-fetcher/frontend
   export ALCHEMY_API_KEY=your_key
   ./eth-fetcher
   ```

4. Access dashboard via `http://:8080/dashboard.html`.

---

## 🗑 Cleanup old job files
```
find /var/eth-fetcher/jobs -type f -mtime +7 -delete
```

---

## 📜 License
MIT
