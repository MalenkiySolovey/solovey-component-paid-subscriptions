package provider

import (
	"context"
	"strings"
	"testing"
)

func TestRenderExternalURLReplacesSafeServerSidePlaceholders(t *testing.T) {
	order := &Order{Id: 10, Amount: 12345, Currency: "RUB", IdempotencyKey: "idem"}
	tariff := &Offer{Id: 20}
	clientID := uint(30)

	got := RenderExternalURL("https://pay.example/o/{orderId}/c/{clientId}/t/{tariffId}?a={amount}&c={currency}&k={key}", order, tariff, clientID)
	for _, want := range []string{"/o/10/", "/c/30/", "/t/20", "a=12345", "c=RUB", "k=idem"} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderExternalURL missing %q in %q", want, got)
		}
	}
}

func TestTelegramProviderInvoiceShape(t *testing.T) {
	provider := NewTelegramProvider(ProviderStars, "")
	invoice, err := provider.CreateInvoice(context.Background(), &Order{IdempotencyKey: "payload", Amount: 50}, &Offer{Name: "Month"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if invoice.Method != InvoiceTelegramNative || invoice.Currency != "XTR" || invoice.Payload != "payload" {
		t.Fatalf("unexpected invoice: %#v", invoice)
	}
	if len(invoice.Prices) != 1 || invoice.Prices[0].Amount != 50 {
		t.Fatalf("unexpected invoice prices: %#v", invoice.Prices)
	}
}
