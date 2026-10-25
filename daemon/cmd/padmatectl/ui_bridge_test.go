package main

import "testing"

func TestUIBridgeMethodAllowlist(t *testing.T) {
	for _, method := range []string{"hello", "rpc", "fake.press", "fake.release", "fake.axis", "fake.effects", "instance.forward", "ui.register", "send", "engine.setLayer", "", "macros.run", "macros",
		// The installer's exit (PKG-04) is for padmatectl quit only.
		"daemon.exit"} {
		if bridgeMethod(method) {
			t.Fatal("unsafe method allowed", method)
		}
	}
	for _, method := range []string{"status.get", "devices.list", "devices.identify", "devices.rawInfo", "devices.setMapping", "devices.resetMapping", "latency.get", "latency.reset", "engine.pause", "engine.resume", "engine.releaseAll", "config.get", "config.put", "profiles.get", "profiles.put", "profiles.validate", "profiles.explain", "profiles.list", "profiles.resolve", "script.describe", "script.registrations", "script.status", "script.reload", "script.logs", "subscribe", "ui.setOutput", "profiles.activate", "profiles.delete", "profiles.copy", "apps.recent", "script.init", "script.import", "profiles.folder", "profiles.history", "profiles.restore", "profiles.export", "profiles.import", "profiles.importAmgp", "capture.start", "capture.stop", "startup.get", "startup.set",
		"macros.list", "macros.create", "macros.import", "macros.rename", "macros.delete", "macros.path", "script.path", "script.detach",
		// PRO-29 and DMN-22 (G9).
		"config.repairSeen", "ui.exitReady"} {
		if !bridgeMethod(method) {
			t.Fatal("UI method missing", method)
		}
	}
}
