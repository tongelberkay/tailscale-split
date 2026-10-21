// Command mkicon writes assets/tailsplit.ico and a preview PNG from the same
// drawing code the tray uses. Run "go generate" after changing the glyph.
package main

import (
	"image/png"
	"log"
	"os"

	"tailsplit/internal/ui"
)

func main() {
	if err := os.MkdirAll("assets", 0o755); err != nil {
		log.Fatal(err)
	}
	ico := ui.ICO(ui.AppColor, 16, 20, 24, 32, 40, 48, 64, 256)
	if err := os.WriteFile("assets/tailsplit.ico", ico, 0o644); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create("assets/tailsplit.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, ui.Render(512, ui.AppColor)); err != nil {
		log.Fatal(err)
	}
}
