package runtimecore

import (
	"reflect"
	"testing"

	enginesim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/iniwex5/vowifi-go/internal/vowifi/profile"
)

type preferenceAKAProviderStub struct {
	preferences []string
}

func (provider *preferenceAKAProviderStub) CalculateAKA(_, _ []byte) (enginesim.AKAResult, error) {
	provider.preferences = append(provider.preferences, "default")
	return enginesim.AKAResult{}, nil
}

func (provider *preferenceAKAProviderStub) CalculateAKAWithPreference(_, _ []byte, preference string) (enginesim.AKAResult, error) {
	provider.preferences = append(provider.preferences, preference)
	return enginesim.AKAResult{}, nil
}

func TestReaderSIMAdapterKeepsEPDGAndIMSApplicationSelectionIndependent(t *testing.T) {
	provider := &preferenceAKAProviderStub{}
	adapter := NewReaderSIMAdapter(provider)
	plan := profile.AuthPlan{
		EPDGApp: profile.AKAAppUSIM,
		IMSApp:  profile.AKAAppISIMStrict,
	}

	if _, err := adapter.EPDGSIMProvider(plan).CalculateAKA(nil, nil); err != nil {
		t.Fatalf("EPDG CalculateAKA() error = %v", err)
	}
	if _, err := adapter.IMSAKAProvider(plan).CalculateAKA(nil, nil); err != nil {
		t.Fatalf("IMS CalculateAKA() error = %v", err)
	}

	want := []string{profile.AKAAppUSIM, profile.AKAAppISIMStrict}
	if !reflect.DeepEqual(provider.preferences, want) {
		t.Fatalf("AKA application preferences = %v, want %v", provider.preferences, want)
	}
}
