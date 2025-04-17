package observer

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/ebifrost"
)

func NewEventClient(client ebifrost.LocalhostBifrostClient) *EventClient {
	return &EventClient{
		logger:   log.With().Str("module", "event_client").Logger(),
		client:   client,
		done:     make(chan struct{}),
		handlers: make(map[string]func(*ebifrost.EventNotification)),
	}
}

type EventClient struct {
	logger     zerolog.Logger
	client     ebifrost.LocalhostBifrostClient
	done       chan struct{}
	eventTypes []string
	handlers   map[string]func(*ebifrost.EventNotification)
	mu         sync.RWMutex
}

// RegisterHandler registers a function to handle events of a specific type
func (ec *EventClient) RegisterHandler(eventType string, handler func(*ebifrost.EventNotification)) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.handlers[eventType] = handler

	// Add to event types if not already there
	found := false
	for _, t := range ec.eventTypes {
		if t == eventType {
			found = true
			break
		}
	}
	if !found {
		ec.eventTypes = append(ec.eventTypes, eventType)
	}
}

// Start begins listening for events with automatic reconnection
func (ec *EventClient) Start() {
	go ec.subscribeWithRetry()
}

// Stop ends the event subscription
func (ec *EventClient) Stop() {
	close(ec.done)
}

func (ec *EventClient) subscribeWithRetry() {
	backoff := time.Second
	maxBackoff := 2 * time.Minute

	for {
		select {
		case <-ec.done:
			return
		default:
			if err := ec.subscribe(); err != nil {
				ec.logger.Error().Err(err).Msg("Subscription error")

				// Wait before reconnecting with exponential backoff
				select {
				case <-ec.done:
					return
				case <-time.After(backoff):
					// Increase backoff for next attempt, with a maximum
					backoff = time.Duration(math.Min(
						float64(backoff*2),
						float64(maxBackoff),
					))
				}
			} else {
				// Reset backoff on successful connection
				backoff = time.Second
			}
		}
	}
}

func (ec *EventClient) subscribe() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a separate goroutine to cancel the context when done
	go func() {
		select {
		case <-ec.done:
			cancel()
		case <-ctx.Done():
			// Context already done
		}
	}()

	// Get a copy of event types under read lock
	ec.mu.RLock()
	eventTypes := make([]string, len(ec.eventTypes))
	copy(eventTypes, ec.eventTypes)
	ec.mu.RUnlock()

	stream, err := ec.client.SubscribeToEvents(ctx, &ebifrost.SubscribeRequest{
		EventTypes: eventTypes,
	})
	if err != nil {
		return err
	}

	for {
		event, err := stream.Recv()
		if err != nil {
			return err
		}

		// Get the appropriate handler under read lock
		ec.mu.RLock()
		handler, exists := ec.handlers[event.EventType]
		ec.mu.RUnlock()

		if exists {
			// Call handler in a goroutine to prevent blocking
			go handler(event)
		}
	}
}
