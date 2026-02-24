package adapters

import (
	"context"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/push"
)

type fakeAdapter struct {
	called int
}

func (f *fakeAdapter) Send(context.Context, push.PushToken, push.PushPayload) error {
	f.called++
	return nil
}

func TestMultiAdapterSendRoutesByPlatform(t *testing.T) {
	apns := &fakeAdapter{}
	fcm := &fakeAdapter{}
	multi := NewMultiAdapter(apns, fcm)

	if err := multi.Send(context.Background(), push.PushToken{Platform: "apns"}, push.PushPayload{}); err != nil {
		t.Fatalf("apns send error: %v", err)
	}
	if apns.called != 1 || fcm.called != 0 {
		t.Fatalf("apns/fcm counts = %d/%d, want 1/0", apns.called, fcm.called)
	}

	if err := multi.Send(context.Background(), push.PushToken{Platform: "fcm"}, push.PushPayload{}); err != nil {
		t.Fatalf("fcm send error: %v", err)
	}
	if apns.called != 1 || fcm.called != 1 {
		t.Fatalf("apns/fcm counts = %d/%d, want 1/1", apns.called, fcm.called)
	}
}
