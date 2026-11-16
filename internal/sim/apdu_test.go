package sim

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	swusim "github.com/iniwex5/vowifi-go/engine/sim"
)

func TestParseUSIMAuthResponseRejectsEmptySuccessResponseClearly(t *testing.T) {
	_, err := ParseUSIMAuthResponse("wwan0", []byte{0x90, 0x00})
	if err == nil || !strings.Contains(err.Error(), "缺少数据体") {
		t.Fatalf("ParseUSIMAuthResponse() error = %v, want empty response diagnostic", err)
	}
	if strings.Contains(err.Error(), "非 9000") {
		t.Fatalf("ParseUSIMAuthResponse() error = %v, must not call 9000 a non-success status", err)
	}
}

func TestParseUSIMAuthResponseSyncFailureReturnsErrSyncFailureWithAUTS(t *testing.T) {
	auts := bytes.Repeat([]byte{0xCC}, 14)
	resp := append([]byte{0xDC, 0x0E}, auts...)
	resp = append(resp, 0x90, 0x00)

	got, err := ParseUSIMAuthResponse("wwan0", resp)
	if !errors.Is(err, swusim.ErrSyncFailure) {
		t.Fatalf("err = %v, want ErrSyncFailure", err)
	}
	if !bytes.Equal(got.AUTS, auts) {
		t.Fatalf("AUTS = %s, want %s", hex.EncodeToString(got.AUTS), hex.EncodeToString(auts))
	}
	if len(got.RES) != 0 {
		t.Fatalf("RES = %s, want empty on sync failure", hex.EncodeToString(got.RES))
	}
}
