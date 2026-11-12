module github.com/Pheodaron/n-projects/apps/desktop

go 1.27.3

require (
	connectrpc.com/connect v1.18.1
	github.com/Pheodaron/n-projects v0.0.0
	github.com/wailsapp/wails/v3 v3.0.0
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/Pheodaron/n-projects => ../..

replace github.com/wailsapp/wails/v3 => github.com/Piloswine1/wails/v3 v3.0.0-20261029002042-1d1c0c36d0bc
