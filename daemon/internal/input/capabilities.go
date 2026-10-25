package input

import "github.com/Zyko0/go-sdl3/sdl"

// readCapabilities runs only on T-input with an opened gamepad. Reading
// properties does not enable sensors, rumble or LEDs. SDL 3.4.16 gamepad
// property macros alias the joystick property strings (SDL_gamepad.h).
func readCapabilities(gp *sdl.Gamepad) Capabilities {
	props, err := gp.Properties()
	if err != nil {
		return Capabilities{}
	}
	rgb := props.BooleanProperty("SDL.joystick.cap.rgb_led", false)
	return Capabilities{
		Known:         true,
		Gyro:          gp.HasSensor(sdl.SENSOR_GYRO) || gp.HasSensor(sdl.SENSOR_GYRO_L) || gp.HasSensor(sdl.SENSOR_GYRO_R),
		Accelerometer: gp.HasSensor(sdl.SENSOR_ACCEL) || gp.HasSensor(sdl.SENSOR_ACCEL_L) || gp.HasSensor(sdl.SENSOR_ACCEL_R),
		Touchpads:     max(0, gp.NumTouchpads()),
		Rumble:        props.BooleanProperty("SDL.joystick.cap.rumble", false),
		TriggerRumble: props.BooleanProperty("SDL.joystick.cap.trigger_rumble", false),
		LED:           rgb || props.BooleanProperty("SDL.joystick.cap.mono_led", false),
		RGBLED:        rgb,
		PlayerLED:     props.BooleanProperty("SDL.joystick.cap.player_led", false),
	}
}
