package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/url"
	"strings"

	"github.com/Pheodaron/n-projects/apps/desktop/frontend"
	"github.com/Pheodaron/n-projects/apps/desktop/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	backendURL := flag.String("backend-url", "http://127.0.0.1:8080", "Backend HTTP(S) URL")
	token := flag.String("token", "dev-token", "Backend access token")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected arguments; use --help for available options")
	}
	if err := validateBackendURL(*backendURL); err != nil {
		log.Fatal(err)
	}
	if strings.TrimSpace(*token) == "" {
		log.Fatal("--token must not be empty")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge := desktop.New(ctx, *backendURL, *token)
	app := application.New(application.Options{Name: "n-projects", Services: []application.Service{application.NewService(bridge)}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(frontend.Assets)}})
	bridge.Emit = func(name, data string) { app.Event.Emit(name, data) }
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "n-projects", Width: 1180, Height: 800, URL: "/"})
	if e := app.Run(); e != nil {
		log.Fatal(e)
	}
}

func validateBackendURL(rawURL string) error {
	endpoint, err := url.Parse(rawURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("--backend-url must be an HTTP(S) URL without credentials, query or fragment")
	}
	return nil
}
