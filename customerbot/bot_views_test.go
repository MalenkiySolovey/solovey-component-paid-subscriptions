package customerbot

import (
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in      string
		wantCmd string
		wantArg string
	}{
		{"/start", "/start", ""},
		{"/start@MyBot code123", "/start", "code123"},
		{"/QR", "/qr", ""},
		{"  /stats  ", "/stats", ""},
		{"hello there", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		cmd, arg := parseCommand(tc.in)
		if cmd != tc.wantCmd || arg != tc.wantArg {
			t.Errorf("parseCommand(%q) = (%q,%q), want (%q,%q)", tc.in, cmd, arg, tc.wantCmd, tc.wantArg)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		1023:    "1023 B",
		1024:    "1.00 KiB",
		1536:    "1.50 KiB",
		1 << 20: "1.00 MiB",
		1 << 30: "1.00 GiB",
		-5:      "0 B",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestProgressBar(t *testing.T) {
	if got := progressBar(0); got != "[░░░░░░░░░░]" {
		t.Errorf("progressBar(0) = %q", got)
	}
	if got := progressBar(100); got != "[██████████]" {
		t.Errorf("progressBar(100) = %q", got)
	}
	if got := progressBar(150); got != "[██████████]" {
		t.Errorf("progressBar(150) clamp = %q", got)
	}
	if got := progressBar(50); got != "[█████░░░░░]" {
		t.Errorf("progressBar(50) = %q", got)
	}
}

func TestPickLang(t *testing.T) {
	if pickLanguage("ru") != Russian {
		t.Error("ru should map to Russian")
	}
	if pickLanguage("ru-RU") != Russian {
		t.Error("ru-RU should map to Russian")
	}
	if pickLanguage("en-US") != English {
		t.Error("en-US should map to English")
	}
	if pickLanguage("") != English {
		t.Error("empty should map to English")
	}
}

func TestChunkText(t *testing.T) {
	if got := chunkText("short", 100); len(got) != 1 || got[0] != "short" {
		t.Errorf("chunkText short = %v", got)
	}
	big := "aaaa\nbbbb\ncccc\ndddd"
	chunks := chunkText(big, 9)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %v", chunks)
	}
	for _, ch := range chunks {
		if len(ch) > 9 {
			t.Errorf("chunk too long: %q", ch)
		}
	}
	// A single line longer than max must be hard-split so no chunk exceeds max.
	for _, ch := range chunkText(strings.Repeat("x", 25), 10) {
		if len([]rune(ch)) > 10 {
			t.Errorf("oversized line not hard-split: %q (%d)", ch, len([]rune(ch)))
		}
	}
}

func TestFormatOrderAmount(t *testing.T) {
	if got := formatOrderAmount(5, "XTR"); got != "5 ⭐" {
		t.Errorf("XTR amount = %q, want %q", got, "5 ⭐")
	}
	if got := formatOrderAmount(10000, "RUB"); got != "100.00 RUB" {
		t.Errorf("RUB amount = %q, want %q", got, "100.00 RUB")
	}
}

func TestOrderStatusLabelFallback(t *testing.T) {
	if got := orderStatusLabel("paid", English); got != "paid" {
		t.Errorf("paid label = %q", got)
	}
	if got := orderStatusLabel("weird", English); got != "weird" {
		t.Errorf("unknown status should fall back to raw value, got %q", got)
	}
}

func callbackSet(kb *Keyboard) map[string]bool {
	m := map[string]bool{}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData != "" {
				m[b.CallbackData] = true
			}
		}
	}
	return m
}

func TestMenuKeyboards(t *testing.T) {
	b := &Handler{}
	main := callbackSet(MenuKeyboard(English))
	for _, want := range []string{"links", "qr", "stats", "payment", "help"} {
		if !main[want] {
			t.Errorf("main menu missing callback %q", want)
		}
	}
	// "buy" moved into the payment submenu; it must not be on the top level.
	if main["buy"] {
		t.Error("top-level menu should no longer expose 'buy'")
	}
	pay := callbackSet(b.paymentMenuKeyboard(English))
	for _, want := range []string{"buy", "orders", "refund", "menu"} {
		if !pay[want] {
			t.Errorf("payment submenu missing callback %q", want)
		}
	}
}
