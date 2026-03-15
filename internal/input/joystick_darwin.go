package input

/*
#cgo darwin LDFLAGS: -framework IOKit -framework CoreFoundation

#include <IOKit/hid/IOHIDManager.h>
#include <CoreFoundation/CoreFoundation.h>
#include <string.h>

// HID usage pages and usages
#define kUsagePage_GenericDesktop 0x01
#define kUsagePage_Button        0x09
#define kUsage_Joystick          0x04
#define kUsage_GamePad           0x05
#define kUsage_X                 0x30
#define kUsage_Y                 0x31
#define kUsage_Z                 0x32
#define kUsage_Rx                0x33
#define kUsage_Ry                0x34
#define kUsage_Rz                0x35
#define kUsage_Slider            0x36
#define kUsage_Dial              0x37
#define kUsage_Hatswitch         0x39

typedef struct {
    IOHIDManagerRef manager;
    IOHIDDeviceRef  device;
    IOHIDElementRef axes[4];
    int32_t         axisMin[4];
    int32_t         axisMax[4];
    IOHIDElementRef buttons[12];
    int             numAxes;
    int             numButtons;
    char            name[256];
    int             connected;
} JoyHandle;

// Map a usage to our axis index (0=X, 1=Y, 2=Rz/twist, 3=Slider/throttle)
static int usage_to_axis(uint32_t usage) {
    switch (usage) {
        case kUsage_X:      return 0;
        case kUsage_Y:      return 1;
        case kUsage_Rz:     return 2;
        case kUsage_Z:      return 2;  // fallback
        case kUsage_Slider: return 3;
        case kUsage_Dial:   return 3;  // fallback
        case kUsage_Rx:     return 2;  // fallback
        default:            return -1;
    }
}

static CFMutableDictionaryRef create_matching_dict(uint32_t usagePage, uint32_t usage) {
    CFMutableDictionaryRef dict = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFNumberRef pageRef = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &usagePage);
    CFNumberRef usageRef = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &usage);
    CFDictionarySetValue(dict, CFSTR(kIOHIDDeviceUsagePageKey), pageRef);
    CFDictionarySetValue(dict, CFSTR(kIOHIDDeviceUsageKey), usageRef);
    CFRelease(pageRef);
    CFRelease(usageRef);
    return dict;
}

static int joy_init(JoyHandle *h) {
    memset(h, 0, sizeof(JoyHandle));

    h->manager = IOHIDManagerCreate(kCFAllocatorDefault, kIOHIDOptionsTypeNone);
    if (!h->manager) return -1;

    // Match joysticks and gamepads
    CFMutableDictionaryRef matchJoy = create_matching_dict(kUsagePage_GenericDesktop, kUsage_Joystick);
    CFMutableDictionaryRef matchPad = create_matching_dict(kUsagePage_GenericDesktop, kUsage_GamePad);
    CFDictionaryRef matches[] = { matchJoy, matchPad };
    CFArrayRef matchArray = CFArrayCreate(kCFAllocatorDefault, (const void **)matches, 2, &kCFTypeArrayCallBacks);
    IOHIDManagerSetDeviceMatchingMultiple(h->manager, matchArray);
    CFRelease(matchArray);
    CFRelease(matchJoy);
    CFRelease(matchPad);

    // Schedule on current run loop and open
    IOHIDManagerScheduleWithRunLoop(h->manager, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
    IOReturn ret = IOHIDManagerOpen(h->manager, kIOHIDOptionsTypeNone);
    if (ret != kIOReturnSuccess) {
        CFRelease(h->manager);
        h->manager = NULL;
        return -2;
    }

    // Process events to discover devices
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, false);

    // Get first device
    CFSetRef devSet = IOHIDManagerCopyDevices(h->manager);
    if (!devSet || CFSetGetCount(devSet) == 0) {
        if (devSet) CFRelease(devSet);
        return -3;
    }

    CFIndex count = CFSetGetCount(devSet);
    IOHIDDeviceRef *devices = (IOHIDDeviceRef *)malloc(sizeof(IOHIDDeviceRef) * count);
    CFSetGetValues(devSet, (const void **)devices);
    h->device = devices[0];
    CFRetain(h->device);
    free(devices);
    CFRelease(devSet);

    // Get device name
    CFStringRef nameRef = IOHIDDeviceGetProperty(h->device, CFSTR(kIOHIDProductKey));
    if (nameRef) {
        CFStringGetCString(nameRef, h->name, sizeof(h->name), kCFStringEncodingUTF8);
    } else {
        strcpy(h->name, "Unknown Joystick");
    }

    // Enumerate elements
    CFArrayRef elements = IOHIDDeviceCopyMatchingElements(h->device, NULL, kIOHIDOptionsTypeNone);
    if (!elements) return -4;

    h->numAxes = 0;
    h->numButtons = 0;

    for (CFIndex i = 0; i < CFArrayGetCount(elements); i++) {
        IOHIDElementRef elem = (IOHIDElementRef)CFArrayGetValueAtIndex(elements, i);
        IOHIDElementType type = IOHIDElementGetType(elem);
        uint32_t page = IOHIDElementGetUsagePage(elem);
        uint32_t usage = IOHIDElementGetUsage(elem);

        if (type == kIOHIDElementTypeInput_Misc || type == kIOHIDElementTypeInput_Axis) {
            if (page == kUsagePage_GenericDesktop) {
                int idx = usage_to_axis(usage);
                if (idx >= 0 && idx < 4 && h->axes[idx] == NULL) {
                    h->axes[idx] = elem;
                    CFRetain(elem);
                    h->axisMin[idx] = (int32_t)IOHIDElementGetLogicalMin(elem);
                    h->axisMax[idx] = (int32_t)IOHIDElementGetLogicalMax(elem);
                    h->numAxes++;
                }
            }
        } else if (type == kIOHIDElementTypeInput_Button) {
            if (page == kUsagePage_Button && usage >= 1 && usage <= 12) {
                int btnIdx = usage - 1;
                if (h->buttons[btnIdx] == NULL) {
                    h->buttons[btnIdx] = elem;
                    CFRetain(elem);
                    h->numButtons++;
                }
            }
        }
    }
    CFRelease(elements);

    h->connected = 1;
    return 0;
}

static int joy_poll(JoyHandle *h, double outAxes[4], int outButtons[12]) {
    if (!h->connected || !h->device) return -1;

    // Pump run loop briefly to get fresh values
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.001, false);

    // Read axes
    for (int i = 0; i < 4; i++) {
        outAxes[i] = 0.0;
        if (h->axes[i]) {
            IOHIDValueRef valueRef = NULL;
            IOReturn ret = IOHIDDeviceGetValue(h->device, h->axes[i], &valueRef);
            if (ret == kIOReturnSuccess && valueRef) {
                CFIndex raw = IOHIDValueGetIntegerValue(valueRef);
                int32_t mn = h->axisMin[i];
                int32_t mx = h->axisMax[i];
                if (mx > mn) {
                    outAxes[i] = ((double)(raw - mn) / (double)(mx - mn)) * 2.0 - 1.0;
                }
            }
        }
    }

    // Read buttons
    for (int i = 0; i < 12; i++) {
        outButtons[i] = 0;
        if (h->buttons[i]) {
            IOHIDValueRef valueRef = NULL;
            IOReturn ret = IOHIDDeviceGetValue(h->device, h->buttons[i], &valueRef);
            if (ret == kIOReturnSuccess && valueRef) {
                outButtons[i] = IOHIDValueGetIntegerValue(valueRef) != 0 ? 1 : 0;
            }
        }
    }

    return 0;
}

static void joy_close(JoyHandle *h) {
    if (h->device) {
        for (int i = 0; i < 4; i++) {
            if (h->axes[i]) CFRelease(h->axes[i]);
        }
        for (int i = 0; i < 12; i++) {
            if (h->buttons[i]) CFRelease(h->buttons[i]);
        }
        CFRelease(h->device);
        h->device = NULL;
    }
    if (h->manager) {
        IOHIDManagerClose(h->manager, kIOHIDOptionsTypeNone);
        IOHIDManagerUnscheduleFromRunLoop(h->manager, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
        CFRelease(h->manager);
        h->manager = NULL;
    }
    h->connected = 0;
}
*/
import "C"

import (
	"context"
	"runtime"
	"time"
)

// PollJoystick uses IOKit HID to read joystick input on macOS.
// Must run on a locked OS thread (IOKit requires consistent run loop thread).
func PollJoystick(ctx context.Context, ch chan JoystickState) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var handle C.JoyHandle
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	retryTicker := time.NewTicker(time.Second)
	defer retryTicker.Stop()

	connected := false

	for {
		select {
		case <-ctx.Done():
			if connected {
				C.joy_close(&handle)
			}
			return
		default:
		}

		if !connected {
			ret := C.joy_init(&handle)
			if ret != 0 {
				sendLatest(ch, JoystickState{Connected: false})
				select {
				case <-ctx.Done():
					return
				case <-retryTicker.C:
					continue
				}
			}
			connected = true
		}

		select {
		case <-ctx.Done():
			C.joy_close(&handle)
			return
		case <-ticker.C:
		}

		var axes [4]C.double
		var buttons [12]C.int

		ret := C.joy_poll(&handle, &axes[0], &buttons[0])
		if ret != 0 {
			C.joy_close(&handle)
			connected = false
			sendLatest(ch, JoystickState{Connected: false})
			continue
		}

		var state JoystickState
		state.Connected = true
		state.Name = C.GoString(&handle.name[0])
		for i := 0; i < 4; i++ {
			state.Axes[i] = float64(axes[i])
		}
		for i := 0; i < 12; i++ {
			state.Buttons[i] = buttons[i] != 0
		}

		sendLatest(ch, state)
	}
}
