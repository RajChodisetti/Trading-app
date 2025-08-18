package observ

import (
	"encoding/json"
	"fmt"
	"time"
)

func Log(event string, kv map[string]any) {
	if kv == nil {
		kv = map[string]any{}
	}
	kv["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	kv["event"] = event
	b, _ := json.Marshal(kv)
	fmt.Println(string(b))
}
