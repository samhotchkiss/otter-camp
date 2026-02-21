package api

import (
	"sync"

	"github.com/samhotchkiss/otter-camp/internal/ws"
)

var (
	openClawHandlerRegistryMu sync.RWMutex
	openClawHandlerRegistry   *ws.OpenClawHandler
)

func registerOpenClawHandler(handler *ws.OpenClawHandler) {
	openClawHandlerRegistryMu.Lock()
	openClawHandlerRegistry = handler
	openClawHandlerRegistryMu.Unlock()
}

func OpenClawHandlerForRuntime() *ws.OpenClawHandler {
	openClawHandlerRegistryMu.RLock()
	defer openClawHandlerRegistryMu.RUnlock()
	return openClawHandlerRegistry
}
