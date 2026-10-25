package main

import "github.com/wdcyxxycdw/padmate/daemon/internal/ipc"

// hudEvents is the overlay of the scripts' hud API (SCR-22): an IPC client
// subscribing to the topic hud, which the UI does while its overlay is up.
// Toasts go out as events; panels are in the slots of the periodic state
// events (rpcSlot.Panels), so a missed event heals itself. Neither method
// blocks: Publish queues and drops when a client is behind.
type hudEvents struct{ hub *ipc.Hub }

// rpcHUDToast is a toast of a script, its text already one line and cut,
// ms how long it stays.
type rpcHUDToast struct {
	Kind string `json:"kind"`
	Slot uint8  `json:"slot"`
	Text string `json:"text"`
	MS   int    `json:"ms"`
}

func (e hudEvents) Connected() bool { return e.hub.Listening("hud") }

func (e hudEvents) Toast(slot uint8, text string, ms int) {
	e.hub.Publish("hud", rpcHUDToast{Kind: "toast", Slot: slot, Text: text, MS: ms})
}
