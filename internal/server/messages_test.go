package server

import (
	"testing"

	"github.com/Iryoda/ktpls/internal/messages"
)

func TestNearestKey(t *testing.T) {
	b := &messages.Bundles{Keys: map[string][]messages.Entry{
		"user.not-found": nil, "user.not-founds": nil, "order.not-found": nil, "user.disabled": nil,
	}}
	for typo, want := range map[string]string{
		"user.not-foundx": "user.not-found", // not user.not-founds, as far
		"user.nt-found":   "user.not-found",
		"order.notfound":  "order.not-found",
		"usr.disabled":    "user.disabled",
		"payment.failed":  "",
		"Invalid price":   "", // a message, not a key
		"":                "",
	} {
		if got := nearestKey(b, typo); got != want {
			t.Errorf("nearestKey(%q) = %q, want %q", typo, got, want)
		}
	}
}
