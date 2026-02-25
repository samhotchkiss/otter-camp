package security

import (
	"context"
	"log/slog"
	"strings"
)

type ScrubbingHandler struct {
	next     slog.Handler
	scrubber *SecretScrubber
}

func NewScrubbingHandler(next slog.Handler, scrubber *SecretScrubber) *ScrubbingHandler {
	if scrubber == nil {
		scrubber = NewSecretScrubber()
	}
	return &ScrubbingHandler{next: next, scrubber: scrubber}
}

func (h *ScrubbingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *ScrubbingHandler) Handle(ctx context.Context, record slog.Record) error {
	cleaned := slog.NewRecord(record.Time, record.Level, h.scrubString("msg", record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		cleaned.AddAttrs(h.scrubAttr(attr))
		return true
	})
	return h.next.Handle(ctx, cleaned)
}

func (h *ScrubbingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	scrubbed := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		scrubbed = append(scrubbed, h.scrubAttr(attr))
	}
	return &ScrubbingHandler{next: h.next.WithAttrs(scrubbed), scrubber: h.scrubber}
}

func (h *ScrubbingHandler) WithGroup(name string) slog.Handler {
	return &ScrubbingHandler{next: h.next.WithGroup(name), scrubber: h.scrubber}
}

func (h *ScrubbingHandler) scrubAttr(attr slog.Attr) slog.Attr {
	value := attr.Value
	switch value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(h.scrubString(attr.Key, value.String()))
	case slog.KindAny:
		if typed, ok := value.Any().(map[string]any); ok {
			attr.Value = slog.AnyValue(h.scrubber.ScrubMap(typed))
		}
	case slog.KindGroup:
		group := value.Group()
		scrubbed := make([]slog.Attr, 0, len(group))
		for _, item := range group {
			scrubbed = append(scrubbed, h.scrubAttr(item))
		}
		attr.Value = slog.GroupValue(scrubbed...)
	}
	return attr
}

func (h *ScrubbingHandler) scrubString(key, value string) string {
	cleaned := h.scrubber.Scrub(value)
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if strings.Contains(lowerKey, "prompt") || strings.Contains(lowerKey, "response") || strings.Contains(lowerKey, "content") {
		if len(cleaned) > 200 {
			return cleaned[:200] + "[truncated]"
		}
	}
	return cleaned
}
