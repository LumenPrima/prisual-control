package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
)

// Linux joystick event: /usr/include/linux/joystick.h
// struct js_event {
//     __u32 time;   // timestamp in ms
//     __s16 value;  // axis/button value
//     __u8  type;   // event type
//     __u8  number; // axis/button number
// };
type jsEvent struct {
	Time   uint32
	Value  int16
	Type   uint8
	Number uint8
}

const (
	jsEventButton = 0x01
	jsEventAxis   = 0x02
	jsEventInit   = 0x80
)

type axisStats struct {
	min      int16
	max      int16
	seen     map[int16]bool
	current  int16
	prev     int16
	hasPrev  bool
	minDelta uint16
}

func main() {
	devPath := "/dev/input/js0"
	if len(os.Args) > 1 {
		devPath = os.Args[1]
	}

	f, err := os.Open(devPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open %s: %v\n", devPath, err)
		fmt.Fprintln(os.Stderr, "Usage: joyprobe-raw [/dev/input/jsN]")
		os.Exit(1)
	}
	defer f.Close()

	fmt.Printf("Reading raw events from %s\n", devPath)
	fmt.Println("Move every axis through its FULL range, then press Ctrl+C.")
	fmt.Println()

	axes := make(map[int]*axisStats)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	done := make(chan struct{})
	go func() {
		<-sig
		close(done)
	}()

	// Read events
	var ev jsEvent
	for {
		select {
		case <-done:
			printReport(axes)
			return
		default:
		}

		err := binary.Read(f, binary.LittleEndian, &ev)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nRead error: %v\n", err)
			printReport(axes)
			return
		}

		// Only care about axis events (strip init flag)
		if ev.Type&jsEventAxis == 0 {
			continue
		}

		num := int(ev.Number)
		a, ok := axes[num]
		if !ok {
			a = &axisStats{
				seen:     make(map[int16]bool),
				min:      ev.Value,
				max:      ev.Value,
				minDelta: math.MaxUint16,
			}
			axes[num] = a
		}

		v := ev.Value
		a.current = v
		a.seen[v] = true
		if v < a.min {
			a.min = v
		}
		if v > a.max {
			a.max = v
		}

		// Init events set initial values, don't track delta from them
		if ev.Type&jsEventInit != 0 {
			a.prev = v
			a.hasPrev = true
			continue
		}

		if a.hasPrev && v != a.prev {
			d := int32(v) - int32(a.prev)
			if d < 0 {
				d = -d
			}
			if uint16(d) < a.minDelta {
				a.minDelta = uint16(d)
			}
		}
		a.prev = v
		a.hasPrev = true

		// Live display
		fmt.Print("\r")
		for i := 0; i < maxAxis(axes)+1; i++ {
			if aa, ok := axes[i]; ok {
				fmt.Printf("  A%d: %+6d (%4d vals)", i, aa.current, len(aa.seen))
			}
		}
		fmt.Print("   ")
	}
}

func maxAxis(axes map[int]*axisStats) int {
	m := 0
	for k := range axes {
		if k > m {
			m = k
		}
	}
	return m
}

func printReport(axes map[int]*axisStats) {
	fmt.Println()
	fmt.Println()
	fmt.Println("=== Raw /dev/input/js* Axis Report ===")
	fmt.Println()

	for i := 0; i <= maxAxis(axes); i++ {
		a, ok := axes[i]
		if !ok {
			continue
		}
		discrete := len(a.seen)
		rawRange := int(a.max) - int(a.min)
		bits := 0
		for b := discrete; b > 1; b >>= 1 {
			bits++
		}

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
