package garageeventstream

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.albinodrought/creamy-discord-ha-webhooks/internal/eventstream"
)

type DoorState int

const (
	DoorStateUnknown DoorState = iota
	DoorStateOpen
	DoorStateClosed
	DoorStateOpening
	DoorStateClosing
)

func (s DoorState) String() string {
	switch s {
	case DoorStateOpen:
		return "Open"
	case DoorStateClosed:
		return "Closed"
	case DoorStateOpening:
		return "Opening"
	case DoorStateClosing:
		return "Closing"
	default:
		return "Unknown"
	}
}

type Event struct {
	State  StreamState
	Mapped DoorState
}

type StreamState struct {
	ID               string  `json:"id"`
	Value            float32 `json:"value"`
	State            string  `json:"state"`
	CurrentOperation string  `json:"current_operation"`
	Position         float32 `json:"position"`
}

type GarageEventStream struct {
	cancel context.CancelFunc
	url    string

	events chan (Event)

	pollLock sync.Mutex
}

func (ges *GarageEventStream) Close() error {
	if ges.cancel != nil {
		ges.cancel()
	}
	return nil
}

func (ges *GarageEventStream) Poll() error {
	return ges.poll()
}

func (ges *GarageEventStream) poll() error {
	ges.pollLock.Lock()
	defer ges.pollLock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	ges.cancel = cancel

	req, err := http.NewRequestWithContext(ctx, "GET", ges.url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	parser := eventstream.NewParser(resp.Body)
	for {
		event, err := parser.Next()
		if err != nil {
			return err
		}
		if event.Type != "state" {
			continue
		}
		if !strings.HasPrefix(event.Data, `{"id":"cover-door",`) {
			continue
		}
		var gstate StreamState
		if err := json.Unmarshal([]byte(event.Data), &gstate); err != nil {
			return err
		}

		log.Printf("received garage door update: %+v", gstate)
		mapped := DoorStateUnknown
		if gstate.State == "OPEN" && gstate.CurrentOperation == "IDLE" {
			mapped = DoorStateOpen
		} else if gstate.State == "OPEN" && gstate.CurrentOperation == "CLOSING" {
			mapped = DoorStateClosing
		} else if gstate.State == "OPEN" && gstate.CurrentOperation == "OPENING" {
			mapped = DoorStateOpening
		} else if gstate.State == "CLOSED" && gstate.CurrentOperation == "IDLE" {
			mapped = DoorStateClosed
		}
		select {
		case ges.events <- Event{State: gstate, Mapped: mapped}:
		default:
		}
	}
}

func New(url string, events chan (Event)) *GarageEventStream {
	return &GarageEventStream{
		url:    url,
		events: events,
	}
}
