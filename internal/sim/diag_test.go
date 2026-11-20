package sim

import (
	"strings"
	"testing"
)

func TestSensitiveHexDiagnosticsNeverExposeFragments(t *testing.T) {
	secret := "00112233445566778899aabbccddeeff"
	for name, masked := range map[string]string{
		"bytes":  maskHexBytes([]byte(secret)),
		"string": maskHexString(secret),
	} {
		for _, fragment := range []string{"001122", "ddeeff", secret} {
			if strings.Contains(masked, fragment) {
				t.Fatalf("%s mask %q leaked %q", name, masked, fragment)
			}
		}
		if !strings.Contains(masked, "redacted") {
			t.Fatalf("%s mask %q does not identify redaction", name, masked)
		}
	}
}
