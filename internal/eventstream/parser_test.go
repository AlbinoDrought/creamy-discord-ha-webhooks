package eventstream

import (
	"strings"
	"testing"
)

func TestParser_Next(t *testing.T) {
	input := `retry: 30000
id: 444261369
event: ping
data: {"title":"test"}

event: state
data: {"id":"sensor","value":42}

`

	parser := NewParser(strings.NewReader(input))
	
	event1, err := parser.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if event1.Retry != 30000 {
		t.Errorf("expected retry 30000, got %d", event1.Retry)
	}
	if event1.ID != "444261369" {
		t.Errorf("expected id '444261369', got '%s'", event1.ID)
	}
	if event1.Type != "ping" {
		t.Errorf("expected type 'ping', got '%s'", event1.Type)
	}
	if event1.Data != `{"title":"test"}` {
		t.Errorf("expected data '{\"title\":\"test\"}', got '%s'", event1.Data)
	}
	
	event2, err := parser.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if event2.Type != "state" {
		t.Errorf("expected type 'state', got '%s'", event2.Type)
	}
	if event2.Data != `{"id":"sensor","value":42}` {
		t.Errorf("expected data '{\"id\":\"sensor\",\"value\":42}', got '%s'", event2.Data)
	}
}

func TestParser_ParseAll(t *testing.T) {
	input := `event: test1
data: data1

event: test2
data: data2

`

	parser := NewParser(strings.NewReader(input))
	events, err := parser.ParseAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	
	if events[0].Type != "test1" || events[0].Data != "data1" {
		t.Errorf("unexpected first event: %+v", events[0])
	}
	
	if events[1].Type != "test2" || events[1].Data != "data2" {
		t.Errorf("unexpected second event: %+v", events[1])
	}
}

func TestParser_MultilineData(t *testing.T) {
	input := `event: multiline
data: line1
data: line2

`

	parser := NewParser(strings.NewReader(input))
	event, err := parser.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	expected := "line1\nline2"
	if event.Data != expected {
		t.Errorf("expected data '%s', got '%s'", expected, event.Data)
	}
}

func TestParser_EmptyData(t *testing.T) {
	input := `event: ping
data: 

`

	parser := NewParser(strings.NewReader(input))
	event, err := parser.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if event.Type != "ping" {
		t.Errorf("expected type 'ping', got '%s'", event.Type)
	}
	if event.Data != "" {
		t.Errorf("expected empty data, got '%s'", event.Data)
	}
}