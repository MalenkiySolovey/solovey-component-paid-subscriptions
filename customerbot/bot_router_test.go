package customerbot

import (
	"context"
	"strings"
	"testing"
)

func TestSubscriptionButtonUsesOnlyTheCallersPrivateChat(t *testing.T) {
	const user int64 = 7
	reads, sends := 0, 0
	handler := Handler{
		Allow: func(id int64) bool { return id == user },
		Subscription: func(id int64) (string, []string, error) {
			reads++
			if id != user {
				t.Fatalf("subscription lookup for the wrong Telegram user: %d", id)
			}
			return "https://subscriptions.example.test/current", nil, nil
		},
		Send: Sender{
			AnswerCallback: func(context.Context, string, string) error { return nil },
			Message: func(_ context.Context, chatID int64, text string, _ *Keyboard) error {
				sends++
				if chatID != user || !strings.Contains(text, "https://subscriptions.example.test/current") {
					t.Fatalf("wrong private reply to %d: %q", chatID, text)
				}
				return nil
			},
		},
	}
	for _, chat := range []Chat{{Type: "group", ID: -13}, {Type: "private", ID: 14}} {
		handler.HandleUpdate(t.Context(), &Update{CallbackQuery: &CallbackQuery{From: User{ID: user}, Data: "links", Message: &Message{Chat: chat}}})
	}
	if reads != 0 || sends != 0 {
		t.Fatal("subscription button acted outside the caller's private chat")
	}
	handler.HandleUpdate(t.Context(), &Update{CallbackQuery: &CallbackQuery{From: User{ID: user}, Data: "links", Message: &Message{Chat: Chat{Type: "private", ID: user}}}})
	if reads != 1 || sends != 1 {
		t.Fatal("valid private callback did not deliver a subscription")
	}
}
