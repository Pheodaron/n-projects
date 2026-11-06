package main

import (
	"context"
	"github.com/Pheodaron/n-projects/frontend"
	"github.com/Pheodaron/n-projects/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"log"
	"os"
)

func main() {
	url := os.Getenv("BACKEND_URL")
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	if os.Getenv("DEV_TOKEN") == "" {
		log.Fatal("DEV_TOKEN is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge := desktop.New(ctx, url, os.Getenv("DEV_TOKEN"))
	app := application.New(application.Options{Name: "n-projects", Services: []application.Service{application.NewService(bridge)}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(frontend.Assets)}})
	bridge.Emit = func(name, data string) { app.Event.Emit(name, data) }
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "n-projects", Width: 1180, Height: 800, URL: "/"})
	if e := app.Run(); e != nil {
		log.Fatal(e)
	}
}
