package eventstream

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

var (
	ErrInvalidFormat = errors.New("invalid event stream format")
)

type Event struct {
	ID    string
	Type  string
	Data  string
	Retry int
}

type Parser struct {
	scanner *bufio.Scanner
}

func NewParser(r io.Reader) *Parser {
	return &Parser{
		scanner: bufio.NewScanner(r),
	}
}

func (p *Parser) Next() (*Event, error) {
	event := &Event{}
	
	for p.scanner.Scan() {
		line := p.scanner.Text()
		
		if line == "" {
			if event.Type != "" || event.Data != "" || event.ID != "" || event.Retry > 0 {
				return event, nil
			}
			continue
		}
		
		if !strings.Contains(line, ":") {
			continue
		}
		
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		
		field := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		
		switch field {
		case "event":
			event.Type = value
		case "data":
			if event.Data != "" {
				event.Data += "\n" + value
			} else {
				event.Data = value
			}
		case "id":
			event.ID = value
		case "retry":
			retry, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			event.Retry = retry
		}
	}
	
	if err := p.scanner.Err(); err != nil {
		return nil, err
	}
	
	if event.Type != "" || event.Data != "" || event.ID != "" || event.Retry > 0 {
		return event, nil
	}
	
	return nil, io.EOF
}

func (p *Parser) ParseAll() ([]*Event, error) {
	var events []*Event
	
	for {
		event, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	
	return events, nil
}