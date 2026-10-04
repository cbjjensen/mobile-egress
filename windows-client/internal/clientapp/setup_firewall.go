package clientapp

import (
	"context"
	"errors"
)

func (app *App) CheckFirewall() (FirewallStatus, error) {
	service, ok := app.service.(FirewallService)
	if !ok {
		return FirewallStatus{}, errors.New("Update the Client service to check its firewall access.")
	}
	return service.CheckFirewall(context.Background())
}

func (app *App) RetryFirewall() (FirewallStatus, error) {
	service, ok := app.service.(FirewallService)
	if !ok {
		return FirewallStatus{}, errors.New("Update the Client service to manage its firewall access.")
	}
	return service.RetryFirewall(context.Background())
}

func (client LocalClient) CheckFirewall(ctx context.Context) (FirewallStatus, error) {
	return client.firewallCall(ctx, "check-firewall")
}

func (client LocalClient) RetryFirewall(ctx context.Context) (FirewallStatus, error) {
	return client.firewallCall(ctx, "retry-firewall")
}

func (client LocalClient) firewallCall(ctx context.Context, method string) (FirewallStatus, error) {
	response, err := client.call(ctx, method, "")
	if err != nil {
		return FirewallStatus{}, err
	}
	if response.Firewall == nil {
		return FirewallStatus{}, errors.New("Client firewall status is unavailable.")
	}
	return *response.Firewall, nil
}
