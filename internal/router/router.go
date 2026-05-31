package router

import (
	"prisual_control/internal/visca"
)

// CameraRouter manages which camera the joystick controls based on
// vMix tally state and override button.
type CameraRouter struct {
	Cameras   map[int]*visca.Camera // input number -> camera worker
	CameraIPs map[int]string        // input number -> IP
	Program   int                   // current program input (1-based, 0=none)
	Preview   int                   // current preview input (1-based, 0=none)
	Override  bool                  // override button held (control program camera)
}

// TargetInput returns the input number that the joystick should control.
// Default: preview camera. With override: program camera.
func (r *CameraRouter) TargetInput() int {
	if r.Override && r.Program > 0 {
		return r.Program
	}
	if r.Preview > 0 {
		return r.Preview
	}
	return 0
}

// ActiveCamera returns the camera worker for the currently targeted input.
// Returns nil if no camera is targeted or the input has no connection.
func (r *CameraRouter) ActiveCamera() *visca.Camera {
	target := r.TargetInput()
	if target == 0 {
		return nil
	}
	return r.Cameras[target]
}

// UpdateTally updates the program/preview state and returns whether the
// active target changed.
func (r *CameraRouter) UpdateTally(program, preview int) (changed bool) {
	oldTarget := r.TargetInput()
	r.Program = program
	r.Preview = preview
	return r.TargetInput() != oldTarget
}

// HandleSwitch stops all motion on the old camera when the target changes.
// Call this when UpdateTally returns true.
func (r *CameraRouter) HandleSwitch(oldTarget int) {
	if oldTarget > 0 {
		if cam := r.Cameras[oldTarget]; cam != nil {
			visca.StopAllMotion(cam)
		}
	}
}

// SetOverride updates the override state and returns whether the active
// target changed.
func (r *CameraRouter) SetOverride(held bool) (changed bool) {
	oldTarget := r.TargetInput()
	r.Override = held
	return r.TargetInput() != oldTarget
}

// TallyState returns "PROGRAM", "PREVIEW", or "" for a given input number.
func (r *CameraRouter) TallyState(inputNum int) string {
	if inputNum == r.Program {
		return "PROGRAM"
	}
	if inputNum == r.Preview {
		return "PREVIEW"
	}
	return ""
}

// InputNumbers returns a sorted list of all input numbers with connections.
func (r *CameraRouter) InputNumbers() []int {
	nums := make([]int, 0, len(r.Cameras))
	for n := range r.Cameras {
		nums = append(nums, n)
	}
	// Simple insertion sort (small N)
	for i := 1; i < len(nums); i++ {
		for j := i; j > 0 && nums[j] < nums[j-1]; j-- {
			nums[j], nums[j-1] = nums[j-1], nums[j]
		}
	}
	return nums
}
