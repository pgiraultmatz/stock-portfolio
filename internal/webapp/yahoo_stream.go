package webapp

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const yahooStreamerURL = "wss://streamer.finance.yahoo.com/?version=2"

type yahooStreamQuote struct {
	Symbol        string
	Price         float64
	Time          int64
	MarketHours   int
	ChangePercent float64
	Change        float64
}

func fetchYahooStreamQuotes(ctx context.Context, symbols []string, wait time.Duration) map[string]yahooStreamQuote {
	const batchSize = 40
	results := make(map[string]yahooStreamQuote)
	if len(symbols) == 0 {
		return results
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for start := 0; start < len(symbols); start += batchSize {
		end := min(start+batchSize, len(symbols))
		batch := append([]string(nil), symbols[start:end]...)
		wg.Add(1)
		go func() {
			defer wg.Done()
			quotes := fetchYahooStreamQuoteBatch(ctx, batch, wait)
			mu.Lock()
			for symbol, quote := range quotes {
				results[symbol] = quote
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func fetchYahooStreamQuoteBatch(ctx context.Context, symbols []string, wait time.Duration) map[string]yahooStreamQuote {
	results := make(map[string]yahooStreamQuote)
	normalized := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol != "" {
			normalized = append(normalized, symbol)
		}
	}
	if len(normalized) == 0 {
		return results
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 3 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}
	headers := http.Header{}
	headers.Set("User-Agent", "Mozilla/5.0")
	conn, _, err := dialer.DialContext(ctx, yahooStreamerURL, headers)
	if err != nil {
		return results
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string][]string{"subscribe": normalized}); err != nil {
		return results
	}

	deadline := time.Now().Add(wait)
	_ = conn.SetReadDeadline(deadline)
	for len(results) < len(normalized) && time.Now().Before(deadline) {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			break
		}
		quote, ok := decodeYahooStreamQuote(payload)
		if ok {
			results[quote.Symbol] = quote
		}
	}
	return results
}

func decodeYahooStreamQuote(payload []byte) (yahooStreamQuote, bool) {
	var encoded string
	if len(payload) > 0 && payload[0] == '{' {
		var envelope struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return yahooStreamQuote{}, false
		}
		encoded = envelope.Message
	} else if len(payload) > 0 && payload[0] == '"' {
		if err := json.Unmarshal(payload, &encoded); err != nil {
			return yahooStreamQuote{}, false
		}
	} else {
		encoded = strings.TrimSpace(string(payload))
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return yahooStreamQuote{}, false
	}

	var quote yahooStreamQuote
	for len(raw) > 0 {
		tag, n := binary.Uvarint(raw)
		if n <= 0 {
			return yahooStreamQuote{}, false
		}
		raw = raw[n:]
		field, wire := int(tag>>3), int(tag&7)
		switch {
		case field == 1 && wire == 2:
			value, rest, ok := protobufBytes(raw)
			if !ok {
				return yahooStreamQuote{}, false
			}
			quote.Symbol = string(value)
			raw = rest
		case (field == 2 || field == 8 || field == 12) && wire == 5:
			if len(raw) < 4 {
				return yahooStreamQuote{}, false
			}
			value := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[:4])))
			switch field {
			case 2:
				quote.Price = value
			case 8:
				quote.ChangePercent = value
			case 12:
				quote.Change = value
			}
			raw = raw[4:]
		case (field == 3 || field == 7) && wire == 0:
			value, size := binary.Uvarint(raw)
			if size <= 0 {
				return yahooStreamQuote{}, false
			}
			if field == 3 {
				quote.Time = int64(value>>1) ^ -int64(value&1)
			} else {
				quote.MarketHours = int(value)
			}
			raw = raw[size:]
		default:
			var ok bool
			raw, ok = skipProtobufField(raw, wire)
			if !ok {
				return yahooStreamQuote{}, false
			}
		}
	}
	return quote, quote.Symbol != "" && quote.Price > 0
}

func protobufBytes(raw []byte) ([]byte, []byte, bool) {
	length, n := binary.Uvarint(raw)
	if n <= 0 || length > uint64(len(raw)-n) {
		return nil, nil, false
	}
	start := n
	end := start + int(length)
	return raw[start:end], raw[end:], true
}

func skipProtobufField(raw []byte, wire int) ([]byte, bool) {
	switch wire {
	case 0:
		_, n := binary.Uvarint(raw)
		if n <= 0 {
			return nil, false
		}
		return raw[n:], true
	case 1:
		if len(raw) < 8 {
			return nil, false
		}
		return raw[8:], true
	case 2:
		_, rest, ok := protobufBytes(raw)
		return rest, ok
	case 5:
		if len(raw) < 4 {
			return nil, false
		}
		return raw[4:], true
	default:
		return nil, false
	}
}

func chartExtendedMarketFromStream(quote yahooStreamQuote) *ChartExtendedMarket {
	session := ""
	state := ""
	switch quote.MarketHours {
	case 0:
		session, state = "pre", "PRE"
	case 2:
		session, state = "post", "POST"
	case 3, 4:
		session, state = "overnight", "OVERNIGHT"
	default:
		return nil
	}
	return &ChartExtendedMarket{
		Session:       session,
		MarketState:   state,
		Price:         quote.Price,
		Change:        quote.Change,
		ChangePercent: quote.ChangePercent,
		Time:          quote.Time / 1000,
	}
}
