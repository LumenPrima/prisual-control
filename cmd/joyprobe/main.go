package main

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

type axisStats struct {
	min     int16
	max     int16
	seen    map[int16]bool
	current int16
	prev    int16
	hasPrev bool
	minDelta uint16 // smallest non-zero change between consecutive readings
}

func main() {
	if err := sdl.Init(sdl.INIT_JOYSTICK); err != nil {
		fmt.Fprintf(os.Stderr, "SDL2 init failed: %v\n", err)
		os.Exit(1)
	}
	defer sdl.Quit()

	n := sdl.NumJoysticks()
	if n <= 0 {
		fmt.Fprintln(os.Stderr, "No joysticks found.")
		os.Exit(1)
	}

	joy := sdl.JoystickOpen(0)
	if joy == nil {
		fmt.Fprintln(os.Stderr, "Failed to open joystick 0.")
		os.Exit(1)
	}
	defer joy.Close()

	numAxes := joy.NumAxes()
	numButtons := joy.NumButtons()
	numHats := joy.NumHats()

	fmt.Printf("Joystick: %s\n", joy.Name())
	fmt.Printf("  Axes: %d  Buttons: %d  Hats: %d\n", numAxes, numButtons, numHats)
	fmt.Printf("  GUID: %s\n", sdl.JoystickGetGUIDString(joy.GUID()))
	fmt.Println()
	fmt.Println("Move every axis through its FULL range, then press Ctrl+C.")
	fmt.Println()

	axes := make([]axisStats, numAxes)
	for i := range axes {
		axes[i].seen = make(map[int16]bool)
		axes[i].minDelta = math.MaxUint16
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sig:
			printReport(axes)
			return
		case <-ticker.C:
		}

		sdl.PumpEvents()

		for i := 0; i < numAxes; i++ {
			v := joy.Axis(i)
			axes[i].current = v
			axes[i].seen[v] = true
			if v < axes[i].min {
				axes[i].min = v
			}
			if v > axes[i].max {
				axes[i].max = v
			}
			// Track smallest change between consecutive readings
			if axes[i].hasPrev && v != axes[i].prev {
				d := int32(v) - int32(axes[i].prev)
				if d < 0 {
					d = -d
				}
				if uint16(d) < axes[i].minDelta {
					axes[i].minDelta = uint16(d)
				}
			}
			axes[i].prev = v
			axes[i].hasPrev = true
		}

		// Live display
		fmt.Print("\r")
		for i := 0; i < numAxes; i++ {
			fmt.Printf("  A%d: %+6d (%4d vals)", i, axes[i].current, len(axes[i].seen))
		}
		fmt.Print("   ")
	}
}

func printReport(axes []axisStats) {
	fmt.Println()
	fmt.Println()
	fmt.Println("=== SDL2 Axis Report ===")
	fmt.Println()
	for i, a := range axes {
		discrete := len(a.seen)
		rawRange := int(a.max) - int(a.min)
		bits := 0
		for b := discrete; b > 1; b >>= 1 {
			bits++
		}

		// Compute actual min step from sorted unique values
		vals := make([]int, 0, discrete)
		for v := range a.seen {
			vals = append(vals, int(v))
		}
		sort.Ints(vals)

		minStep := 0
		if len(vals) > 1 {
			minStep = vals[1] - vals[0]
			for j := 2; j < len(vals); j++ {
				d := vals[j] - vals[j-1]
				if d < minStep {
					minStep = d
				}
			}
		}

		avgStep := 0
		if discrete > 1 {
			avgStep = rawRange / (discrete - 1)
		}

		fmt.Printf("Axis %d:\n", i)
		fmt.Printf("  Min: %+6d  Max: %+6d  Range: %d\n", a.min, a.max, rawRange)
		fmt.Printf("  Discrete values seen: %d  (~%d bits)\n", discrete, bits)
		fmt.Printf("  Min step (sorted):      %d\n", minStep)
		if a.minDelta < math.MaxUint16 {
			fmt.Printf("  Min step (consecutive): %d\n", a.minDelta)
		}
		fmt.Printf("  Avg step:               %d  (ideal=1)\n", avgStep)
		fmt.Println()
	}
}
