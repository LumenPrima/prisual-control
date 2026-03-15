package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"

	"prisual_control/internal/input"
)

type axisStats struct {
	min      float64
	max      float64
	seen     map[float64]bool
	current  float64
	prev     float64
	hasPrev  bool
	minDelta float64
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan input.JoystickState, 1)
	go input.PollJoystick(ctx, ch)

	fmt.Println("Waiting for joystick...")

	// Wait for first connected state
	var state input.JoystickState
	for state = range ch {
		if state.Connected {
			break
		}
	}

	fmt.Printf("Joystick: %s\n", state.Name)
	fmt.Println("Move every axis through its FULL range, then press Ctrl+C.")
	fmt.Println()

	axes := make([]axisStats, 4)
	for i := range axes {
		axes[i].seen = make(map[float64]bool)
		axes[i].min = 999
		axes[i].max = -999
		axes[i].minDelta = 999
	}

	// Init from first state
	for i := 0; i < 4; i++ {
		axes[i].current = state.Axes[i]
		axes[i].seen[state.Axes[i]] = true
		axes[i].min = state.Axes[i]
		axes[i].max = state.Axes[i]
		axes[i].prev = state.Axes[i]
		axes[i].hasPrev = true
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	for {
		select {
		case <-sig:
			printReport(axes)
			return
		case state = <-ch:
		}

		if !state.Connected {
			fmt.Println("\nJoystick disconnected!")
			printReport(axes)
			return
		}

		for i := 0; i < 4; i++ {
			v := state.Axes[i]
			axes[i].current = v
			axes[i].seen[v] = true
			if v < axes[i].min {
				axes[i].min = v
			}
			if v > axes[i].max {
				axes[i].max = v
			}
			if axes[i].hasPrev && v != axes[i].prev {
				d := math.Abs(v - axes[i].prev)
				if d < axes[i].minDelta {
					axes[i].minDelta = d
				}
			}
			axes[i].prev = v
			axes[i].hasPrev = true
		}

		fmt.Print("\r")
		for i := 0; i < 4; i++ {
			fmt.Printf("  A%d: %+.4f (%4d vals)", i, axes[i].current, len(axes[i].seen))
		}
		fmt.Print("   ")
	}
}

func printReport(axes []axisStats) {
	fmt.Println()
	fmt.Println()
	fmt.Println("=== Axis Report ===")
	fmt.Println()
	for i, a := range axes {
		discrete := len(a.seen)
		r := a.max - a.min

		vals := make([]float64, 0, discrete)
		for v := range a.seen {
			vals = append(vals, v)
		}
		sort.Float64s(vals)

		minStep := 0.0
		if len(vals) > 1 {
			minStep = vals[1] - vals[0]
			for j := 2; j < len(vals); j++ {
				d := vals[j] - vals[j-1]
				if d < minStep {
					minStep = d
				}
			}
		}

		avgStep := 0.0
		if discrete > 1 {
			avgStep = r / float64(discrete-1)
		}

		focusRange := 4352.0 // 0x1180 - 0x0080
		focusPerStep := focusRange * minStep / 2.0

		fmt.Printf("Axis %d:\n", i)
		fmt.Printf("  Min: %+.6f  Max: %+.6f  Range: %.6f\n", a.min, a.max, r)
		fmt.Printf("  Discrete values seen: %d\n", discrete)
		fmt.Printf("  Min step (sorted):      %.6f\n", minStep)
		if a.minDelta < 999 {
			fmt.Printf("  Min step (consecutive): %.6f\n", a.minDelta)
		}
		fmt.Printf("  Avg step:               %.6f\n", avgStep)
		fmt.Printf("  Focus positions per min step: %.1f\n", focusPerStep)
		fmt.Println()
	}
}
