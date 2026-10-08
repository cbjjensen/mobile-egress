package clientapp

import (
	"context"
	"errors"

	"mobile-egress/windows-client/internal/nodeservice"
)

type HostedService interface {
	StartHostedActivation(context.Context, string) (nodeservice.ActivationView, error)
	ResumeHostedActivation(context.Context) (nodeservice.ActivationView, error)
	CancelHostedActivation(context.Context) error
}

func NewWithBrowser(service Service, clipboard func(string) error, opener func(string) error) *App {
	app := New(service, clipboard)
	app.browser = opener
	return app
}
func (app *App) openActivationBrowser(raw string) error {
	if !nodeservice.ValidActivationURL(raw) {
		return errors.New("The activation browser address is invalid.")
	}
	if app.browser == nil {
		return errors.New("The browser is unavailable. Reopen the Client app to continue activation.")
	}
	return app.browser(raw)
}
func (app *App) hosted() (HostedService, error) {
	if err := app.lifetime.Err(); err != nil {
		return nil, err
	}
	service, ok := app.service.(HostedService)
	if !ok {
		return nil, errors.New("Update the Client service to activate Inevitable.")
	}
	return service, nil
}
func (app *App) StartHostedActivation(name string) (nodeservice.ActivationView, error) {
	service, err := app.hosted()
	if err != nil {
		return nodeservice.ActivationView{}, err
	}
	view, err := service.StartHostedActivation(app.lifetime, name)
	if err != nil {
		return view, err
	}
	if view.VerificationURI != "" {
		err = app.openActivationBrowser(view.VerificationURI)
	}
	return view, err
}
func (app *App) ResumeHostedActivation() (nodeservice.ActivationView, error) {
	service, err := app.hosted()
	if err != nil {
		return nodeservice.ActivationView{}, err
	}
	view, err := service.ResumeHostedActivation(app.lifetime)
	if err != nil {
		return view, err
	}
	if view.VerificationURI != "" {
		err = app.openActivationBrowser(view.VerificationURI)
	}
	return view, err
}
func (app *App) CancelHostedActivation() error {
	service, err := app.hosted()
	if err != nil {
		return err
	}
	return service.CancelHostedActivation(app.lifetime)
}

func (s *firewallService) hosted() (HostedService, error) {
	service, ok := s.DirectService.(HostedService)
	if !ok {
		return nil, errors.New("Update the Client service to activate Inevitable.")
	}
	return service, nil
}
func (s *firewallService) StartHostedActivation(ctx context.Context, name string) (nodeservice.ActivationView, error) {
	service, err := s.hosted()
	if err != nil {
		return nodeservice.ActivationView{}, err
	}
	return service.StartHostedActivation(ctx, name)
}
func (s *firewallService) ResumeHostedActivation(ctx context.Context) (nodeservice.ActivationView, error) {
	service, err := s.hosted()
	if err != nil {
		return nodeservice.ActivationView{}, err
	}
	return service.ResumeHostedActivation(ctx)
}
func (s *firewallService) CancelHostedActivation(ctx context.Context) error {
	service, err := s.hosted()
	if err != nil {
		return err
	}
	return service.CancelHostedActivation(ctx)
}

func (client LocalClient) StartHostedActivation(ctx context.Context, name string) (nodeservice.ActivationView, error) {
	return client.activationCall(ctx, "start-hosted-activation", name)
}
func (client LocalClient) ResumeHostedActivation(ctx context.Context) (nodeservice.ActivationView, error) {
	return client.activationCall(ctx, "resume-hosted-activation", "")
}
func (client LocalClient) CancelHostedActivation(ctx context.Context) error {
	_, err := client.call(ctx, "cancel-hosted-activation", "")
	return err
}
func (client LocalClient) activationCall(ctx context.Context, method, value string) (nodeservice.ActivationView, error) {
	response, err := client.call(ctx, method, value)
	if err != nil {
		return nodeservice.ActivationView{}, err
	}
	if response.Activation == nil {
		return nodeservice.ActivationView{}, errors.New("Client activation status is unavailable.")
	}
	return *response.Activation, nil
}
