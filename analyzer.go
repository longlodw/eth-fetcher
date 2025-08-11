package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/longlodw/lazyiterate"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/time/rate"
)

// JSON-RPC request/response structures
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcBlock struct {
	Number        string  `json:"number"`
	GasUsed       string  `json:"gasUsed"`
	BaseFeePerGas string  `json:"baseFeePerGas"`
	Timestamp     string  `json:"timestamp"`
	Transactions  []rpcTx `json:"transactions"`
}

type rpcTx struct {
	GasPrice string `json:"gasPrice"`
	Gas      string `json:"gas"`
}

type jsonRPCResponse[T any] struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      int64   `json:"id"`
	Result  T       `json:"result"`
	Error   *rpcErr `json:"error"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Analyzer struct {
	alchURL string
	client  *http.Client
	limiter *rate.Limiter
	db      *sql.DB
}

func NewAnalyzer(apiKey string, dbPath string) *Analyzer {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		panic(err)
	}
	// Create cache table if not exists
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS block_cache (
		block_num INTEGER PRIMARY KEY,
		timestamp INTEGER,
		gas_used TEXT,
		total_tips TEXT
	);
	`)
	if err != nil {
		panic(err)
	}
	return &Analyzer{
		alchURL: fmt.Sprintf("https://eth-mainnet.g.alchemy.com/v2/%s", apiKey),
		client:  &http.Client{Timeout: 15 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(25), 25), // 25 req/sec
		db:      db,
	}
}

func (a *Analyzer) getBlockWithTxs(ctx context.Context, blockNum uint64) (*rpcBlock, error) {
	if err := a.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	hexNum := fmt.Sprintf("0x%x", blockNum)
	reqObj := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      time.Now().UnixNano(),
		Method:  "eth_getBlockByNumber",
		Params:  []any{hexNum, true}, // full txs
	}
	reqBody, _ := json.Marshal(reqObj)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.alchURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rpcRes jsonRPCResponse[rpcBlock]
	if err := json.NewDecoder(resp.Body).Decode(&rpcRes); err != nil {
		return nil, err
	}
	if rpcRes.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", rpcRes.Error.Message)
	}
	return &rpcRes.Result, nil
}

func (a *Analyzer) calculateTotalTips(block *rpcBlock) *big.Int {
	baseFee := hexToBig(block.BaseFeePerGas)
	totalTips := lazyiterate.Reduce(
		lazyiterate.Map(
			slices.Values(block.Transactions),
			func(tx rpcTx) *big.Int {
				gasPrice := hexToBig(tx.GasPrice)
				gasUsed := hexToBig(tx.Gas)
				tip := new(big.Int).Sub(gasPrice, baseFee)
				if tip.Sign() < 0 {
					tip.SetInt64(0) // Ensure no negative tips
				}
				return new(big.Int).Mul(tip, gasUsed) // Total tip for this tx
			},
		),
		func(acc, v *big.Int) *big.Int {
			return acc.Add(acc, v)
		},
		big.NewInt(0),
	)
	return totalTips
}

func (a *Analyzer) getBlockGasUsed(block *rpcBlock) *big.Int {
	return hexToBig(block.GasUsed)
}

func (a *Analyzer) GetBlockGasAndTips(ctx context.Context, blockNum uint64) (timestamp time.Time, gasUsed *big.Int, totalTips *big.Int) {
	// Try cache first (cancellable)
	row := a.db.QueryRowContext(ctx, "SELECT timestamp, gas_used, total_tips FROM block_cache WHERE block_num = ?", blockNum)
	var gasUsedStr, totalTipsStr string
	var tsInt int64
	err := row.Scan(&tsInt, &gasUsedStr, &totalTipsStr)
	if err == nil {
		gasUsed = hexToBig(gasUsedStr)
		totalTips = hexToBig(totalTipsStr)
		timestamp = time.Unix(tsInt, 0)
		return timestamp, gasUsed, totalTips
	}
	if err != sql.ErrNoRows {
		// If context cancelled or other error
		if ctx.Err() != nil {
			return time.Time{}, nil, nil
		}
		fmt.Printf("Cache error: %v\n", err)
	}
	numRetried := 0
	for {
		block, err := a.getBlockWithTxs(ctx, blockNum)
		if err != nil && ctx.Err() != nil {
			return time.Time{}, nil, nil // Context cancelled
		}
		if err != nil {
			fmt.Printf("Error fetching block %d: %v\n", blockNum, err)
			time.Sleep(time.Second * time.Duration(2<<numRetried)) // Exponential backoff
			continue
		}

		gasUsed = a.getBlockGasUsed(block)
		totalTips = a.calculateTotalTips(block)
		tsInt, err = strconv.ParseInt(strings.TrimPrefix(block.Timestamp, "0x"), 16, 64)
		if err != nil {
			panic(err)
		}
		timestamp = time.Unix(tsInt, 0)

		// Save to cache
		_, err = a.db.Exec("INSERT OR REPLACE INTO block_cache (block_num, timestamp, gas_used, total_tips) VALUES (?, ?, ?, ?)",
			blockNum, tsInt, block.GasUsed, fmt.Sprintf("0x%x", totalTips))
		if err != nil {
			fmt.Printf("Cache insert error: %v\n", err)
		}
		return timestamp, gasUsed, totalTips
	}
}

func hexToBig(h string) *big.Int {
	h = strings.TrimPrefix(h, "0x")
	if h == "" {
		return big.NewInt(0)
	}
	b, err := hex.DecodeString(padOdd(h))
	if err != nil {
		panic(fmt.Sprintf("invalid hex string: %s", h))
	}
	return new(big.Int).SetBytes(b)
}

func padOdd(s string) string {
	if len(s)%2 == 1 {
		return "0" + s
	}
	return s
}
