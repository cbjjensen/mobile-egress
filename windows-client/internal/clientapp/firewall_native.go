package clientapp

import (
	"context"
	"errors"
)

type firewallRunner func(context.Context, string, []string, []string) ([]byte, error)

// Keep native output bounded and private; callers return fixed user messages.
type firewallOutput struct{ data []byte }

func (w *firewallOutput) Write(p []byte) (int, error) {
	if len(w.data)+len(p) > 16*1024 {
		return 0, errors.New("firewall output exceeded limit")
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

func firewallResult(state, scope string, port uint16) FirewallStatus {
	messages := map[string]string{
		"allowed":     "The Client host firewall exception is allowed. Pair a phone to verify the network connection.",
		"disabled":    "The host application firewall is disabled. Other network controls may still block the connection.",
		"blocked":     "Host firewall policy blocks or does not allow the Client. Review local or managed firewall policy, then retry.",
		"unavailable": "The host firewall could not be checked or changed. Retry or review the Client installation and local firewall settings.",
		"unknown":     "The host firewall returned an unrecognized result. Review local firewall settings; network access has not been verified.",
	}
	return FirewallStatus{State: state, Scope: scope, Port: port, Message: messages[state]}
}
