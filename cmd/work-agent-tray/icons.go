package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"github.com/dwatts1772/work-agent-daemon/internal/core"
)

// statusColors are the tray dot colours per overall status.
var statusColors = map[core.Status]color.NRGBA{
	core.StatusOK:              {0x22, 0xa0, 0x4b, 0xff}, // green
	core.StatusHeldWakes:       {0xe0, 0xa1, 0x00, 0xff}, // amber
	core.StatusOrcaUnavailable: {0x80, 0x80, 0x80, 0xff}, // grey
	core.StatusNeedsOperator:   {0xd1, 0x24, 0x2f, 0xff}, // red
}

// statusTooltips describe each overall status in the tray tooltip.
var statusTooltips = map[core.Status]string{
	core.StatusOK:              "Work Agent: ok",
	core.StatusHeldWakes:       "Work Agent: Held Wakes",
	core.StatusOrcaUnavailable: "Work Agent: Orca unavailable",
	core.StatusNeedsOperator:   "Work Agent: needs Operator",
}

// trayIcon renders the tray icon for s: a filled dot in the status colour.
func trayIcon(s core.Status) []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	c := statusColors[s]
	center, radius := float64(size-1)/2, float64(size)/2-2
	for y := range size {
		for x := range size {
			dx, dy := float64(x)-center, float64(y)-center
			if dx*dx+dy*dy <= radius*radius {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err) // encoding an in-memory image cannot fail
	}
	return buf.Bytes()
}
