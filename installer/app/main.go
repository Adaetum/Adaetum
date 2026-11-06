// Command app is the setup window. It draws what the engine asks and returns the
// operator's answers; every decision about order, validation and dry runs lives
// in installer/engine, so the window and the headless command cannot drift apart.
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Adaetum setup",
		Width:     1100,
		Height:    760,
		MinWidth:  720,
		MinHeight: 520,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 17, B: 19, A: 1},
		// The sidebar runs under the macOS title bar, so the traffic lights sit on it
		// and a drag strip in the page moves the window.
		Mac:       &mac.Options{TitleBar: mac.TitleBarHiddenInset()},
		OnStartup: app.startup,
		Bind:      []interface{}{app},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
