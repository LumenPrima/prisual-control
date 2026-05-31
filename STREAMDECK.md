# Stream Deck XL — PTZ Shim Quick Reference

## Layout

```
Row 0:  [ P1  ] [ P2  ] [ P3  ] [ P4  ] [ P5  ] [ P6  ] [ P7  ] [ P8  ]
Row 1:  [IN 1 ] [IN 2 ] [IN 3 ] [IN 4 ] [IN 5 ] [Z OUT] [ UP  ] [Z IN ]
Row 2:  [ S<  ] [ S>  ] [ FAR ] [ NEAR] [ LT  ] [ LEFT] [HOME ] [RIGHT]
Row 3:  [LIVE ] [     ] [DRIVE] [FOCUS] [ CUT ] [FADE ] [DOWN ] [ SET ]
        [     ] [     ] [LIVE ] [     ] [     ] [     ] [     ] [     ]
```

## Input Selection (Rows 1-2, left side)

These keys select which video source goes to **preview** (the next shot). Tally colors show you what's where:

- **Red** = currently live (program)
- **Green** = on deck (preview)
- **Dark** = not selected

Press a key to put that source on preview, then use CUT or FADE to take it live.

## Transitions

- **CUT** — instant switch from preview to live
- **FADE** — smooth crossfade to preview

## Camera Presets (Row 0)

- **P1–P8** — tap to recall a saved camera position
- The active preset lights up **blue**
- Moving the camera clears the highlight

## Saving a Preset

1. Position the camera where you want it
2. Press **SET** (bottom-right, yellow) — all preset keys turn yellow
3. Press the preset key (P1–P8) you want to save to
4. Done — press any non-preset key to cancel instead

## Camera Movement (Right side D-pad)

- **UP / DOWN / LEFT / RIGHT** — hold to pan/tilt, release to stop
- **Z IN / Z OUT** — hold to zoom, release to stop
- **HOME** — returns to Preset 1

Speed is **zoom-adaptive** — movements slow down automatically when zoomed in to keep things smooth.

## Focus

- **FOCUS** — hold to autofocus, release to return to manual focus
- The key turns **pink** while held
- Great for quick refocus — hold it, let the camera lock on, release
- **FOCUS FAR / FOCUS NEAR** — hold to jog manual focus, release to stop
- Manual focus jog cancels latched autofocus

## Drive Live

- **DRIVE LIVE** — toggles joystick/PTZ control to the **live camera** instead of preview
- Key turns **red** when active, preset row turns dark red as a warning
- Press again to go back to controlling the preview camera
- **Be careful** — you're moving the camera that's currently on screen!

## Lower Third

- **LOWER THIRD** — toggles the lower-third overlay on/off
- **Yellow** when active, dim when off

## Slides (Proclaim)

- **SLIDE <** — previous slide
- **SLIDE >** — next slide
- Controls Proclaim on the iMac remotely

## Stream

- **LIVE** button (bottom-left) — double-tap to start/stop streaming
- First tap: "tap again to toggle stream" (safety)
- Second tap within 2 seconds: actually starts or stops
- Shows **red "LIVE"** when streaming, dim when off

## Tips

- You don't need the joystick to switch cameras — the Stream Deck handles all input selection and transitions
- The joystick is best for smooth, fine camera moves; the D-pad is for quick repositioning
- Always check the tally colors before hitting CUT/FADE — red is what's live right now
