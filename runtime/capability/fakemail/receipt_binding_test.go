package fakemail

import "testing"

func TestSendReceiptBindsProviderIdentityAndManifest(t *testing.T) {
	manifest := digest([]byte("manifest"))
	receipt := SendReceipt{ProviderMessageID: "sent:1", ManifestDigest: manifest}
	receipt.ReceiptDigest = digest([]byte(receipt.ProviderMessageID + "\x00" + manifest))
	if !validSendReceipt(receipt) || receipt.ManifestDigest != manifest {
		t.Fatal("valid receipt rejected")
	}
	for name, mutate := range map[string]func(*SendReceipt){
		"provider identity": func(value *SendReceipt) { value.ProviderMessageID = "sent:2" },
		"manifest":          func(value *SendReceipt) { value.ManifestDigest = digest([]byte("other")) },
		"receipt digest":    func(value *SendReceipt) { value.ReceiptDigest = digest([]byte("other")) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := receipt
			mutate(&candidate)
			if validSendReceipt(candidate) && candidate.ManifestDigest == manifest {
				t.Fatalf("accepted %+v", candidate)
			}
		})
	}
}
