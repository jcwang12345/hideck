package sim

import (
	"fmt"
	"strings"
)

// maskHexBytes never emits cryptographic material. RAND, AUTN, RES, CK, IK,
// AUTS and APDU bodies remain sensitive even when only a prefix or suffix is
// logged; length is sufficient for diagnostics.
func maskHexBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return fmt.Sprintf("[redacted:%dB]", len(b))
}

// maskHexString keeps only the encoded length and never exposes a fragment.
func maskHexString(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return fmt.Sprintf("[redacted:%dhex]", len(s))
}
