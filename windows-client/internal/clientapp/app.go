package clientapp

import (
	"context"
	"errors"
	"mobile-egress/windows-client/internal/nodeservice"
)

type App struct {
	service   Service
	clipboard func(string) error
}

func New(service Service, clipboard func(string) error) *App {
	return &App{service: service, clipboard: clipboard}
}
func (app *App) Status() nodeservice.StandaloneStatus { return app.service.Status() }
func (app *App) Pair(bundle string) error             { return app.service.Pair(context.Background(), bundle) }
func (app *App) Import(bundle string) error           { return app.service.Import(context.Background(), bundle) }
func (app *App) CopyProxy(kind string) error {
	if kind != "http" && kind != "socks" {
		return errors.New("Unknown proxy format.")
	}
	value, err := app.service.Proxy(context.Background(), kind)
	if err != nil {
		return err
	}
	if app.clipboard == nil {
		return errors.New("Clipboard is unavailable.")
	}
	return app.clipboard(value)
}
