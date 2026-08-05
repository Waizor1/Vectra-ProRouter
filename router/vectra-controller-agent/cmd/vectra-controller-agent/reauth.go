package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/state"
)

// registerWithRecovery registers the router and, if the panel rejects it with
// 403 because it already knows this device (our bearer token was lost), retries
// ONCE with a keypair-signed recovery proof. This converts token loss from a
// permanent brick (operator must delete the panel record by hand) into a
// self-healing event. It falls back to the original error when there is no
// device private key to sign with, or the failure is not a 403.
func registerWithRecovery(
	ctx context.Context,
	client *controlplane.Client,
	persisted *state.PersistedState,
	req controlplane.RegisterRequest,
) (controlplane.RegisterResponse, error) {
	resp, err := client.Register(ctx, req)
	if err == nil {
		return resp, nil
	}

	var statusErr *controlplane.StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusForbidden {
		return controlplane.RegisterResponse{}, err
	}
	if persisted.DevicePrivateKey == "" || persisted.DeviceIdentifier == "" {
		// Nothing to prove identity with; surface the original 403.
		return controlplane.RegisterResponse{}, err
	}

	proof, signErr := controlplane.SignReauth(persisted.DevicePrivateKey, persisted.DeviceIdentifier, time.Now().UTC())
	if signErr != nil {
		log.Printf("keypair recovery signing failed: %v", signErr)
		return controlplane.RegisterResponse{}, err
	}

	req.RecoveryProof = proof
	log.Printf("register rejected (403); retrying with keypair-signed recovery proof for %s", persisted.DeviceIdentifier)
	resp, retryErr := client.Register(ctx, req)
	if retryErr != nil {
		return controlplane.RegisterResponse{}, retryErr
	}
	log.Printf("keypair-signed recovery succeeded; fresh token issued for %s", persisted.DeviceIdentifier)
	return resp, nil
}
