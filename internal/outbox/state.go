package outbox

import (
	"crypto/sha256"
	"fmt"
	"time"
)

func GenerateIdempotencyKey(symbol, intent string, timestamp time.Time, score float64) string {
	data := fmt.Sprintf("%s-%s-%d-%.6f", symbol, intent, timestamp.Unix(), score)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:8])
}

func GenerateOrderID(symbol string, timestamp time.Time) string {
	return fmt.Sprintf("order_%s_%d", symbol, timestamp.UnixNano())
}