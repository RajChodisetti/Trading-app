package stubs

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// SSEServer handles Server-Sent Events streaming for wire stub
type SSEServer struct {
	events     []WireEvent
	clients    map[string]chan WireEvent
	clientsMu  sync.RWMutex
	heartbeat  time.Duration
}

// NewSSEServer creates a new SSE server with the given events
func NewSSEServer(events []WireEvent) *SSEServer {
	return &SSEServer{
		events:    events,
		clients:   make(map[string]chan WireEvent),
		heartbeat: 10 * time.Second,
	}
}

// ServeHTTP handles SSE streaming requests
func (s *SSEServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")
	
	// Get Last-Event-ID for resume
	lastEventID := r.Header.Get("Last-Event-ID")
	startIndex := 0
	
	if lastEventID != "" {
		// Find resume point
		for i, event := range s.events {
			if event.ID == lastEventID {
				startIndex = i + 1
				break
			}
		}
	}
	
	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())
	eventChan := make(chan WireEvent, 100)
	
	// Register client
	s.clientsMu.Lock()
	s.clients[clientID] = eventChan
	s.clientsMu.Unlock()
	
	// Clean up on disconnect
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, clientID)
		close(eventChan)
		s.clientsMu.Unlock()
		log.Printf("SSE client %s disconnected", clientID)
	}()
	
	log.Printf("SSE client %s connected, resuming from index %d", clientID, startIndex)
	
	// Send initial events
	for i := startIndex; i < len(s.events); i++ {
		event := s.events[i]
		if err := s.writeEvent(w, event); err != nil {
			log.Printf("SSE write error: %v", err)
			return
		}
		
		// Add small delay for realistic streaming
		time.Sleep(50 * time.Millisecond)
		
		// Check if client disconnected
		if r.Context().Err() != nil {
			return
		}
	}
	
	// Send server watermark
	if err := s.writeServerWatermark(w); err != nil {
		return
	}
	
	// Keep connection alive with heartbeats
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Send heartbeat comment
			if _, err := fmt.Fprintf(w, ":ping\n\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		case event := <-eventChan:
			// Send new event (for dynamic events in future)
			if err := s.writeEvent(w, event); err != nil {
				return
			}
		}
	}
}

// writeEvent writes a single SSE event to the response
func (s *SSEServer) writeEvent(w http.ResponseWriter, event WireEvent) error {
	// Marshal event payload
	payloadBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	
	// Write SSE format
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", string(payloadBytes)); err != nil {
		return err
	}
	
	// Flush immediately
	w.(http.Flusher).Flush()
	return nil
}

// writeServerWatermark sends a server timestamp for lag calculation
func (s *SSEServer) writeServerWatermark(w http.ResponseWriter) error {
	watermark := map[string]interface{}{
		"server_watermark_utc": time.Now().UTC().Format(time.RFC3339),
		"total_events":         len(s.events),
	}
	
	watermarkBytes, _ := json.Marshal(watermark)
	
	if _, err := fmt.Fprintf(w, "event: watermark\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: watermark-%d\n", time.Now().Unix()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", string(watermarkBytes)); err != nil {
		return err
	}
	
	w.(http.Flusher).Flush()
	return nil
}

// ServeBackfill handles /backfill requests for gap repair
func (s *SSEServer) ServeBackfill(w http.ResponseWriter, r *http.Request) {
	sinceID := r.URL.Query().Get("since_id")
	limitStr := r.URL.Query().Get("limit")
	
	limit := 1000 // default
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	
	var startIndex int
	if sinceID != "" {
		// Find the event after sinceID
		for i, event := range s.events {
			if event.ID == sinceID {
				startIndex = i + 1
				break
			}
		}
	}
	
	// Collect events up to limit
	var backfillEvents []WireEvent
	for i := startIndex; i < len(s.events) && len(backfillEvents) < limit; i++ {
		backfillEvents = append(backfillEvents, s.events[i])
	}
	
	response := map[string]interface{}{
		"events":     backfillEvents,
		"since_id":   sinceID,
		"count":      len(backfillEvents),
		"total":      len(s.events),
		"has_more":   startIndex+len(backfillEvents) < len(s.events),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
	
	log.Printf("Backfill: since_id=%s, returned %d events", sinceID, len(backfillEvents))
}

// BroadcastEvent sends an event to all connected clients (for dynamic events)
func (s *SSEServer) BroadcastEvent(event WireEvent) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	
	for clientID, ch := range s.clients {
		select {
		case ch <- event:
		default:
			log.Printf("Client %s event channel full, dropping event", clientID)
		}
	}
}

// GetConnectedClients returns the number of connected SSE clients
func (s *SSEServer) GetConnectedClients() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}