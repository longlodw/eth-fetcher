package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math/big"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

type BlockResult struct {
	BlockNum  uint64
	TimeStamp time.Time
	GasUsed   *big.Int
	Tips      *big.Int
	Err       error
}

type JobStatus struct {
	Status   string `json:"status"`
	FilePath string `json:"filePath,omitempty"`
	Error    string `json:"error,omitempty"`

	Start uint64 `json:"start"`
	End   uint64 `json:"end"`

	LastWritten uint64             `json:"lastWritten"`
	Cancel      context.CancelFunc `json:"-"` // for stopping the job
}

var (
	jobs   = make(map[string]*JobStatus)
	jobsMu sync.RWMutex
)

// parallelFetcher fetches blocks in parallel batches and writes sorted output to CSV
func parallelFetcher(ctx context.Context, analyzer *Analyzer, start, end uint64, filePath string) error {
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	// Write header once
	writer.Write([]string{"block_number", "timestamp", "gas_used", "tips"})

	const batchSize = 500
	lastWritten := start

	for batchStart := start; batchStart <= end; batchStart += batchSize {
		batchEnd := min(batchStart+batchSize-1, end)

		// Collect this batch in memory only
		batchResults := make([]*BlockResult, 0, batchEnd-batchStart+1)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for bn := batchStart; bn <= batchEnd; bn++ {
			// Check if stop was requested
			select {
			case <-ctx.Done():
				// Stop: exit cleanly, CSV already has lastWritten contiguous data
				return nil
			default:
			}

			wg.Add(1)
			go func(blockNum uint64) {
				defer wg.Done()

				timestamp, gas, tips := analyzer.GetBlockGasAndTips(ctx, blockNum)
				if gas != nil && tips != nil {
					mu.Lock()
					batchResults = append(batchResults, &BlockResult{
						BlockNum:  blockNum,
						TimeStamp: timestamp,
						GasUsed:   gas,
						Tips:      tips,
					})
					mu.Unlock()
				}
			}(bn)
		}

		wg.Wait()

		// Sort the batch by block number
		sort.Slice(batchResults, func(i, j int) bool {
			return batchResults[i].BlockNum < batchResults[j].BlockNum
		})

		// Ensure contiguous write from lastWritten onward
		for _, r := range batchResults {
			if r.BlockNum == lastWritten {
				writer.Write([]string{
					fmt.Sprintf("%d", r.BlockNum),
					r.GasUsed.String(),
					r.Tips.String(),
				})
				lastWritten++
			} else if r.BlockNum > lastWritten {
				// Hit a gap — stop writing this batch
				break
			}
		}
		writer.Flush()
		jobsMu.Lock()
		if job, ok := jobs[ctx.Value("jobID").(string)]; ok {
			job.LastWritten = lastWritten - 1
		}
		jobsMu.Unlock()
	}

	return nil
}

func main() {
	apiKey := os.Getenv("ALCHEMY_API_KEY")
	analyzer := NewAnalyzer(apiKey, "/var/eth-fetcher/results.db")

	// Submit request endpoint
	http.HandleFunc("/request", func(w http.ResponseWriter, r *http.Request) {
		startStr := r.URL.Query().Get("start")
		endStr := r.URL.Query().Get("end")
		start, err := strconv.ParseUint(startStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid start block", 400)
			return
		}
		end, err := strconv.ParseUint(endStr, 10, 64)
		if err != nil || end < start {
			http.Error(w, "Invalid end block", 400)
			return
		}

		jobID := uuid.New().String()
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), "jobID", jobID))

		filePath := fmt.Sprintf("/var/eth-fetcher/jobs/eth_blocks_%d_%d_%s.csv", start, end, jobID)

		jobsMu.Lock()
		jobs[jobID] = &JobStatus{
			Status: "pending",
			Start:  start,
			End:    end,
			Cancel: cancel,
		}
		jobsMu.Unlock()

		go func() {
			err := parallelFetcher(ctx, analyzer, start, end, filePath)
			jobsMu.Lock()
			defer jobsMu.Unlock()
			if err != nil && ctx.Err() != context.Canceled {
				jobs[jobID].Status = "error"
				jobs[jobID].Error = err.Error()
			} else {
				jobs[jobID].Status = "done"
				jobs[jobID].FilePath = filePath
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"jobID": jobID})
	})

	// Stop job endpoint
	http.HandleFunc("/stop/", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Path[len("/stop/"):]
		jobsMu.RLock()
		job, ok := jobs[jobID]
		if ok && job.Status == "pending" {
			job.Cancel() // cancel context
		}
		jobsMu.RUnlock()
		if !ok {
			http.Error(w, "Job not found", 404)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("Stopping job"))
	})

	// Download endpoint
	http.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Path[len("/download/"):]
		jobsMu.RLock()
		job, ok := jobs[jobID]
		defer jobsMu.RUnlock()
		if !ok || (job.Status != "done" && job.Status != "stopped") || job.FilePath == "" {
			http.Error(w, "File not ready or job not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", job.FilePath[len("jobs/"):]))
		http.ServeFile(w, r, job.FilePath)
	})

	// Status endpoint
	http.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Path[len("/status/"):]
		jobsMu.RLock()
		job, ok := jobs[jobID]
		defer jobsMu.RUnlock()
		if !ok {
			http.Error(w, "Job not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(job)
	})

	// List jobs endpoint
	http.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		jobsMu.RLock()
		jobList := slices.Collect(maps.Keys(jobs))
		jobsMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobList)
	})

	// Serve static files for the frontend
	http.Handle("/", http.FileServer(http.Dir("/var/eth-fetcher/frontend")))

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})

	log.Println("Server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
