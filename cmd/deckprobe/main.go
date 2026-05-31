package main

import (
	"fmt"
	"os"

	"prisual_control/internal/vmix"
)

func main() {
	host := "127.0.0.1"
	if len(os.Args) > 1 {
		host = os.Args[1]
	}

	cameras, total, err := vmix.DiscoverCameras(host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%d cameras from %d inputs:\n", len(cameras), total)
	for _, c := range cameras {
		fmt.Printf("  Input %d: %s -> %s\n", c.InputNumber, c.Title, c.IP)
	}

	fmt.Println()
	inputs, err := vmix.DiscoverInputs(host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	for _, inp := range inputs {
		fmt.Printf("  Input %d: [%s] %q\n", inp.Number, inp.Type, inp.Title)
	}
}
