package voice

import "strings"

type terminalOutcome struct {
	Kind       string
	SIPCode    int
	SIPReason  string
	Terminator string
}

func (call *Call) setTerminalOutcome(kind string, sipCode int, sipReason, terminator string) {
	if call == nil {
		return
	}
	call.mu.Lock()
	call.terminalOutcome = terminalOutcome{
		Kind: strings.TrimSpace(kind), SIPCode: sipCode,
		SIPReason: strings.TrimSpace(sipReason), Terminator: strings.TrimSpace(terminator),
	}
	call.mu.Unlock()
}

func (call *Call) setTerminalOutcomeIfEmpty(kind string, sipCode int, sipReason, terminator string) {
	if call == nil {
		return
	}
	call.mu.Lock()
	if strings.TrimSpace(call.terminalOutcome.Kind) == "" {
		call.terminalOutcome = terminalOutcome{
			Kind: strings.TrimSpace(kind), SIPCode: sipCode,
			SIPReason: strings.TrimSpace(sipReason), Terminator: strings.TrimSpace(terminator),
		}
	}
	call.mu.Unlock()
}

func (call *Call) terminalOutcomeSnapshot() terminalOutcome {
	if call == nil {
		return terminalOutcome{}
	}
	call.mu.RLock()
	defer call.mu.RUnlock()
	return call.terminalOutcome
}
