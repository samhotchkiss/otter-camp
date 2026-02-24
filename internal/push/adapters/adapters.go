package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/push"
)

type PushAdapter interface {
	Send(ctx context.Context, token push.PushToken, payload push.PushPayload) error
}

type APNSAdapter struct {
	logger *slog.Logger
}

type FCMAdapter struct {
	logger *slog.Logger
}

type MultiAdapter struct {
	APNS PushAdapter
	FCM  PushAdapter
}

func NewAPNSAdapter(logger *slog.Logger) *APNSAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &APNSAdapter{logger: logger}
}

func NewFCMAdapter(logger *slog.Logger) *FCMAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &FCMAdapter{logger: logger}
}

func NewMultiAdapter(apns PushAdapter, fcm PushAdapter) *MultiAdapter {
	return &MultiAdapter{APNS: apns, FCM: fcm}
}

func (a *APNSAdapter) Send(_ context.Context, token push.PushToken, payload push.PushPayload) error {
	if a == nil {
		return fmt.Errorf("apns adapter is nil")
	}
	a.logger.Info("push dispatch stub", "platform", "apns", "device_id", token.DeviceID, "payload", payload)
	return nil
}

func (a *FCMAdapter) Send(_ context.Context, token push.PushToken, payload push.PushPayload) error {
	if a == nil {
		return fmt.Errorf("fcm adapter is nil")
	}
	a.logger.Info("push dispatch stub", "platform", "fcm", "device_id", token.DeviceID, "payload", payload)
	return nil
}

func (m *MultiAdapter) Send(ctx context.Context, token push.PushToken, payload push.PushPayload) error {
	if m == nil {
		return fmt.Errorf("multi adapter is nil")
	}
	switch strings.ToLower(strings.TrimSpace(token.Platform)) {
	case "apns":
		if m.APNS == nil {
			return fmt.Errorf("apns adapter is not configured")
		}
		return m.APNS.Send(ctx, token, payload)
	case "fcm":
		if m.FCM == nil {
			return fmt.Errorf("fcm adapter is not configured")
		}
		return m.FCM.Send(ctx, token, payload)
	default:
		return fmt.Errorf("unsupported push platform %q", token.Platform)
	}
}
