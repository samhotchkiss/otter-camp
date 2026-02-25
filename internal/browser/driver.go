package browser

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type DriverAction struct {
	Type  string
	Input map[string]any
}

type BrowserDriver interface {
	Execute(ctx context.Context, action DriverAction) (map[string]any, error)
	TakeScreenshot(ctx context.Context) ([]byte, error)
	Close() error
}

type CredentialAwareDriver interface {
	InjectCredentials(ctx context.Context, credentials []BrowserCredential) error
}

type DriverFactory func(ctx context.Context, session BrowserSession) (BrowserDriver, error)

func defaultDriverFactory(_ context.Context, _ BrowserSession) (BrowserDriver, error) {
	return newInMemoryDriver(), nil
}

type inMemoryDriver struct {
	mu         sync.Mutex
	closed     bool
	currentURL string
}

func newInMemoryDriver() *inMemoryDriver {
	return &inMemoryDriver{}
}

func (d *inMemoryDriver) Execute(_ context.Context, action DriverAction) (map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, fmt.Errorf("driver closed")
	}

	actionType := strings.ToLower(strings.TrimSpace(action.Type))
	input := action.Input
	if input == nil {
		input = map[string]any{}
	}

	output := map[string]any{}
	switch actionType {
	case "navigate":
		if raw, ok := input["url"].(string); ok {
			d.currentURL = strings.TrimSpace(raw)
		}
		if d.currentURL != "" {
			output["current_url"] = d.currentURL
		}
	case "back", "forward", "refresh", "click", "type", "select", "hover", "scroll", "press_key", "wait_for_navigation", "evaluate", "wait_for", "extract_text", "extract_structured", "get_page_info", "screenshot":
		if d.currentURL != "" {
			output["current_url"] = d.currentURL
		}
	default:
		return nil, fmt.Errorf("unsupported browser action: %s", actionType)
	}

	if actionType == "evaluate" {
		output["result"] = nil
	}
	return output, nil
}

func (d *inMemoryDriver) TakeScreenshot(context.Context) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, fmt.Errorf("driver closed")
	}
	// 1x1 transparent PNG.
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}, nil
}

func (d *inMemoryDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}
