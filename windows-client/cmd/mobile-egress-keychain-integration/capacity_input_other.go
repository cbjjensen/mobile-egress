//go:build capacityharness && !darwin

package main

import "io"

func protectCapacityInput(io.Reader) (capacityInputProtection, error) {
	return noopCapacityInputProtection{}, nil
}
