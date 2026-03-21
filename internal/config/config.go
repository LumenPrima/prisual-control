package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// ControllerConfig maps physical joystick axes/buttons to logical actions.
type ControllerConfig struct {
	Name    string       `json:"name"`
	Axes    AxesConfig   `json:"axes"`
	Buttons ButtonConfig `json:"buttons"`
}

// AxisMapping maps a physical axis to a logical function.
type AxisMapping struct {
	Index    int     `json:"index"`
	Deadzone float64 `json:"deadzone"`
	Expo     float64 `json:"expo"`
	Inverted bool    `json:"inverted"`
}

// AxesConfig holds mappings for each axis function.
type AxesConfig struct {
	Pan   AxisMapping `json:"pan"`
	Tilt  AxisMapping `json:"tilt"`
	Zoom  AxisMapping `json:"zoom"`
	Focus AxisMapping `json:"focus"`
}

// ButtonConfig holds mappings for each button function.
// Use -1 to leave a function unmapped.
type ButtonConfig struct {
	AFHold          int    `json:"af_hold"`
	AFLatch         int    `json:"af_latch"`
	ProgramOverride int    `json:"program_override"`
	Fade            int    `json:"fade"`
	Cut             int    `json:"cut"`
	CyclePreview    int    `json:"cycle_preview"`
	PresetSave      int    `json:"preset_save"`
	Presets         [6]int `json:"presets"`
}

// DefaultConfig returns the built-in mapping for the Logitech Extreme 3D Pro.
func DefaultConfig() ControllerConfig {
	return ControllerConfig{
		Name: "Logitech Extreme 3D Pro",
		Axes: AxesConfig{
			Pan:   AxisMapping{Index: 0, Deadzone: 0.10, Expo: 2.5, Inverted: false},
			Tilt:  AxisMapping{Index: 1, Deadzone: 0.10, Expo: 2.5, Inverted: true},
			Zoom:  AxisMapping{Index: 2, Deadzone: 0.15, Expo: 2.5, Inverted: false},
			Focus: AxisMapping{Index: 3, Deadzone: 0, Expo: 0, Inverted: false},
		},
		Buttons: ButtonConfig{
			AFHold:          0,
			AFLatch:         1,
			ProgramOverride: 2,
			Fade:            3,
			Cut:             4,
			CyclePreview:    5,
			PresetSave:      1, // same as AFLatch (thumb button, context-dependent)
			Presets:         [6]int{6, 7, 8, 9, 10, 11},
		},
	}
}

// Load reads a controller config from a JSON file.
func Load(path string) (ControllerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ControllerConfig{}, fmt.Errorf("config load: %w", err)
	}

	// Start from defaults so missing fields get sensible values
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ControllerConfig{}, fmt.Errorf("config parse: %w", err)
	}

	return cfg, nil
}

// DumpJSON returns the config as pretty-printed JSON.
func (c ControllerConfig) DumpJSON() string {
	data, _ := json.MarshalIndent(c, "", "  ")
	return string(data)
}

// Save writes the config as pretty-printed JSON to the given path.
func (c ControllerConfig) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config marshal: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("config save: %w", err)
	}
	return nil
}

// BtnMapped returns true if the button index is valid (>= 0).
func BtnMapped(index int) bool {
	return index >= 0
}
