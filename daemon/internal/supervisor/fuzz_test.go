package supervisor_test

import (
	"context"
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// Operations of FuzzRouter. Each takes three bytes: the opcode and
// arguments a and b. Of pads gamepads, pad a%pads is the one an operation
// names. The opcode of a corpus file is its byte modulo frOps, so a new
// operation changes what the files of testdata/fuzz/FuzzRouter mean: rewrite
// their opcode bytes as the operations they were found with (frRemap was added
// as the 14th, after 13).
const (
	frAdd     = iota // pad a connects with a new id, as a twin when b%2 is 1; while connected, a duplicate DeviceAdded
	frRemove         // pad a leaves; while not connected, an unknown device
	frDown           // pad a presses frButtons[b]
	frUp             // pad a releases frButtons[b]
	frAxis           // pad a moves frAxes[a/pads] to int16(b)<<8
	frStall          // slot a%slots blocks in OnEvent on its next event
	frUnstall        // slot a%slots goes on
	frDrop           // input drops a%3+1 events; then a Sync
	frSync           // Sync, checking the properties
	frWait           // the clock moves a ms
	frReload         // Reload, of program2 and program in turn; the check waits for it
	frCombo          // a ComboHeld of pad a's last id, connected or not: the pause toggles
	frSuspend        // the suspension's reasons become a&AllReasons: Suspend, or SuspendQuiet when b%2 is 1
	frRemap          // a DeviceRemapped of pad a's last id (IN-18): connected or not, it is reset when connected
	frOps
)

// frMaxPads is how many gamepads FuzzRouter drives at most: two more than
// the slots, enough for a chain of hand-offs.
const frMaxPads = supervisor.MaxSlots + 2

// frNames and frGUIDs are the pads' own infos; twins share another.
var (
	frNames = [frMaxPads]string{"Pad A", "Pad B", "Pad C", "Pad D", "Pad E", "Pad F"}
	frGUIDs = [frMaxPads]string{"03ab", "057e", "0d1c", "045e", "054c", "2dc8"}
)

// frButtons are the buttons of frDown and frUp: bound, unbound and an
// alias.
var frButtons = [...]string{"south", "east", "west", "leftshoulder", "lb"}

// frAxes are the axes of frAxis: a stick and both triggers.
var frAxes = [...]string{"leftx", "lefttrigger", "righttrigger"}

// frSeeds are the seeds of FuzzRouter: the byte 4*(slots-1)+(EdgeBuffer-1),
// then op, a, b triples.
var frSeeds = [][]byte{
	// tap and hold
	{0, frAdd, 0, 0, frDown, 0, 1, frDown, 0, 0, frUp, 0, 0, frWait, 30, 0, frSync, 0, 0, frUp, 0, 1},
	// behind
	{0, frAdd, 0, 0, frDown, 0, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frDown, 0, 0, frAxis, 0, 0x40, frUp, 0, 1, frSync, 0, 0},
	// a removal while behind, then the next gamepad
	{1, frAdd, 0, 0, frDown, 0, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frDown, 0, 2, frRemove, 0, 0, frAdd, 1, 0, frDown, 1, 1, frSync, 0, 0},
	// a drop while behind
	{0, frAdd, 0, 0, frDown, 0, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frDrop, 0, 0, frDown, 0, 1, frSync, 0, 0},
	// a removal, then a drop, while behind; then it reconnects
	{0, frAdd, 0, 0, frDown, 0, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frRemove, 0, 0, frDrop, 1, 0, frAdd, 0, 0, frSync, 0, 0},
	// the first gamepad presses, so the second waits; it takes the slot
	// and presses, so the first waits when it reconnects; duplicates and
	// unknown devices
	{3, frAdd, 0, 0, frDown, 0, 2, frUp, 0, 2, frAdd, 1, 0, frDown, 1, 1, frRemove, 0, 0, frDown, 1, 0, frRemove, 0, 0, frAdd, 0, 0, frAdd, 0, 0, frDown, 0, 1, frSync, 0, 0},
	// triggers and a stick while behind
	{0, frAdd, 0, 0, frAxis, 3, 0x7f, frStall, 0, 0, frAxis, 3, 0, frAxis, 6, 0x7f, frAxis, 0, 0x80, frAxis, 3, 0x60, frUnstall, 0, 0, frWait, 5, 0, frSync, 0, 0},
	// a hand-off to a gamepad that holds a button and a stick
	{0, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frDown, 1, 1, frAxis, 1, 0x40, frRemove, 0, 0, frSync, 0, 0, frUp, 1, 1, frSync, 0, 0},
	// a reconnect while held, then a tap
	{0, frAdd, 0, 0, frDown, 0, 1, frRemove, 0, 0, frAdd, 0, 0, frDown, 0, 0, frUp, 0, 0, frWait, 30, 0, frSync, 0, 0},
	// a reconnect while held, as XInput reports it (SPK-01): SDL's ups
	// before the removal, then ups with no down on the new id; then a tap
	{0, frAdd, 0, 0, frDown, 0, 1, frDown, 0, 0, frUp, 0, 1, frUp, 0, 0, frRemove, 0, 0, frAdd, 0, 0, frUp, 0, 0, frUp, 0, 1, frSync, 0, 0,
		frDown, 0, 0, frUp, 0, 0, frWait, 30, 0, frSync, 0, 0},
	// a reconnect while held, as Raw Input and HIDAPI report it (SPK-01):
	// a down of the held button soon after the new id is added
	{0, frAdd, 0, 0, frDown, 0, 1, frUp, 0, 1, frRemove, 0, 0, frAdd, 0, 0, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1, frSync, 0, 0},
	// ups with no down from a gamepad that waits and from a stand-in,
	// which then loses the slot to its home
	{0, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frUp, 1, 1, frSync, 0, 0, frUp, 0, 1, frRemove, 0, 0, frUp, 1, 0, frUp, 1, 1, frSync, 0, 0,
		frAdd, 0, 0, frSync, 0, 0, frUp, 1, 1, frSync, 0, 0},
	// the first gamepad reconnects before the second, handed the slot
	// holding a button and a stick, presses one: it takes the slot back,
	// and the second waits again
	{0, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 1, frAxis, 1, 0x40, frRemove, 0, 0, frSync, 0, 0, frAdd, 0, 0, frSync, 0, 0, frDown, 0, 1, frRemove, 1, 0, frSync, 0, 0, frUp, 0, 1},
	// a press confirms the hand-off; the second gamepad is then the home
	// that takes the slot back
	{0, frAdd, 0, 0, frAdd, 1, 0, frRemove, 0, 0, frDown, 1, 0, frUp, 1, 0, frAdd, 0, 0, frSync, 0, 0, frRemove, 1, 0, frSync, 0, 0, frAdd, 1, 0, frSync, 0, 0},
	// the stand-in that loses the slot waits ahead of the gamepad that
	// waited behind it
	{0, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frRemove, 0, 0, frAdd, 0, 0, frSync, 0, 0, frRemove, 0, 0, frSync, 0, 0},
	// a chain of hand-offs keeps the home, which takes the slot back from
	// the last
	{0, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frDown, 0, 2, frUp, 0, 2, frDown, 2, 0, frRemove, 0, 0, frRemove, 1, 0, frSync, 0, 0, frAdd, 0, 0, frAdd, 1, 0, frSync, 0, 0, frRemove, 0, 0, frDown, 2, 1, frAdd, 0, 0, frSync, 0, 0},
	// a hand-off while behind, then a press confirms it
	{1, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frDown, 1, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frRemove, 0, 0, frDown, 1, 0, frSync, 0, 0},
	// the slot taken back while behind, before the hand-off reached it
	{0, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frRemove, 0, 0, frAdd, 0, 0, frDown, 0, 0, frSync, 0, 0},
	// the slot taken back while behind, after the hand-off's DeviceAdded
	// reached it and before its Resync
	{1, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frRemove, 0, 0, frAdd, 0, 0, frSync, 0, 0},
	// identical gamepads: the one that comes back first gets the number,
	// and so the slot, of the one that left first
	{0, frAdd, 0, 1, frAdd, 1, 1, frRemove, 0, 0, frAdd, 0, 1, frRemove, 1, 0, frSync, 0, 0},
	// identical gamepads in a chain: the second takes the slot back under
	// the first one's number
	{0, frAdd, 0, 1, frAdd, 1, 1, frAdd, 2, 1, frRemove, 0, 0, frRemove, 1, 0, frAdd, 1, 1, frSync, 0, 0, frAdd, 0, 1, frSync, 0, 0},
	// a press of the gamepad that waits behind the stand-in, which holds a
	// button, takes the slot over; the home then waits, and when the
	// slot's new gamepad leaves, the stand-in stands in again and loses the
	// slot to the home's press
	{0, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 1, frRemove, 0, 0, frSync, 0, 0, frDown, 2, 0, frSync, 0, 0,
		frAdd, 0, 0, frSync, 0, 0, frRemove, 2, 0, frSync, 0, 0, frDown, 0, 3, frUp, 1, 1, frSync, 0, 0},
	// a take-over while behind
	{1, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 1, frRemove, 0, 0, frStall, 0, 0, frAxis, 1, 0x40, frAxis, 1, 0x50,
		frDown, 2, 1, frUp, 2, 1, frSync, 0, 0},
	// the stand-in's press makes the slot its own, so the press of the
	// gamepad that waits behind it takes nothing over
	{0, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frRemove, 0, 0, frDown, 1, 0, frDown, 2, 1, frSync, 0, 0, frUp, 1, 0, frSync, 0, 0},
	// at startup, the idle gamepad that took the free slot, with a
	// jittering stick, loses it to the press of the one that waits; the
	// press is mapped, and the idle one's press then takes nothing
	{0, frAdd, 0, 0, frAdd, 1, 0, frAxis, 0, 0x08, frAxis, 1, 0x40, frDown, 1, 0, frUp, 1, 0, frSync, 0, 0, frDown, 0, 1, frSync, 0, 0},
	// the gamepad that took the free slot presses first, so it keeps the
	// slot against the press of one that waits
	{0, frAdd, 0, 0, frDown, 0, 2, frUp, 0, 2, frAdd, 1, 0, frDown, 1, 1, frSync, 0, 0, frUp, 1, 1, frSync, 0, 0},
	// the home that takes the slot back is provisional again, so the
	// stand-in's press takes it over again
	{0, frAdd, 0, 0, frDown, 0, 2, frAdd, 1, 0, frRemove, 0, 0, frAdd, 0, 0, frSync, 0, 0, frDown, 1, 0, frSync, 0, 0, frUp, 1, 0, frSync, 0, 0},
	// identical gamepads: the waiting twin's press takes the slot over from
	// the idle one, which then stands in and loses it to the twin's
	// reclaim, and takes it over again with a press
	{0, frAdd, 0, 1, frAdd, 1, 1, frDown, 1, 0, frUp, 1, 0, frSync, 0, 0, frRemove, 1, 0, frAdd, 1, 1, frSync, 0, 0, frDown, 0, 1, frSync, 0, 0},
	// the home takes the slot back and loses it to a press; it then waits
	// before the stand-in, which connected earlier, and stands in first
	{0, frAdd, 0, 0, frDown, 0, 2, frUp, 0, 2, frAdd, 1, 0, frAdd, 2, 0, frRemove, 0, 0, frAdd, 0, 0, frDown, 2, 1, frUp, 2, 1, frSync, 0, 0, frRemove, 2, 0, frSync, 0, 0, frDown, 1, 0, frSync, 0, 0},
	// a Reload while held: the hold and the tap in progress are released,
	// and the controls do nothing until pressed again; then another
	{0, frAdd, 0, 0, frDown, 0, 1, frDown, 0, 0, frReload, 0, 0, frSync, 0, 0, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0, frReload, 0, 0, frUp, 0, 1, frSync, 0, 0},
	// a Reload while behind: the slot catches up with the old program first
	{0, frAdd, 0, 0, frDown, 0, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frDown, 0, 0, frReload, 0, 0, frUp, 0, 1, frSync, 0, 0},
	// a Reload while a stand-in holds a button; the home takes the slot
	// back and holds a button across a second Reload
	{0, frAdd, 0, 0, frDown, 0, 2, frUp, 0, 2, frAdd, 1, 0, frDown, 1, 1, frRemove, 0, 0, frSync, 0, 0, frReload, 0, 0, frAdd, 0, 0, frSync, 0, 0,
		frDown, 0, 1, frReload, 0, 0, frSync, 0, 0, frUp, 0, 1},
	// a Reload, then a take-over of the stand-in, which held a button
	{0, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 1, frRemove, 0, 0, frReload, 0, 0, frDown, 2, 0, frSync, 0, 0, frUp, 1, 1, frSync, 0, 0},
	// a Reload while held, then behind: the slot misses the release and the
	// next press of the canceled control, so its Resync leaves it canceled
	{0, frAdd, 0, 0, frDown, 0, 1, frReload, 0, 0, frSync, 0, 0, frStall, 0, 0, frDown, 0, 0, frDown, 0, 2, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0},
	// the combo while east is held and a tap is under way: both end; a press
	// while paused does nothing, and east, held across the resume, nothing
	// until pressed again (DMN-07)
	{0, frAdd, 0, 0, frDown, 0, 1, frDown, 0, 0, frCombo, 0, 0, frSync, 0, 0, frUp, 0, 0, frDown, 0, 0, frSync, 0, 0, frCombo, 0, 0, frSync, 0, 0,
		frUp, 0, 1, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1},
	// the combo of a gamepad that waits, and of one not connected
	{0, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frCombo, 1, 0, frSync, 0, 0, frCombo, 2, 0, frSync, 0, 0, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0},
	// a pause and a resume while behind: the slot cancels what its Resync
	// brings in, so east, pressed again after the resume, stays canceled
	{0, frAdd, 0, 0, frDown, 0, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frCombo, 0, 0, frUp, 0, 1, frCombo, 0, 0, frDown, 0, 1, frSync, 0, 0,
		frUp, 0, 1, frDown, 0, 1, frSync, 0, 0},
	// paused across a Reload, a removal and a reconnect, and a hand-off
	{0, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frDown, 1, 1, frCombo, 0, 0, frReload, 0, 0, frSync, 0, 0, frRemove, 0, 0, frSync, 0, 0,
		frAdd, 0, 0, frDown, 0, 1, frCombo, 1, 0, frSync, 0, 0, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0},

	// Several slots (IN-08).

	// 2 slots: both hold east, so Space goes down once; one is removed,
	// and Space stays down until the other releases
	{4, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frDown, 1, 1, frSync, 0, 0, frRemove, 0, 0, frSync, 0, 0, frUp, 1, 1, frSync, 0, 0},
	// 2 slots, both final, and C waits holding east: A leaves, so C stands
	// in on slot 0 and presses Space; B leaves, and A, back, takes the free
	// slot 1 and rehomes slot 0 to C, which holds Space on until it
	// releases
	{7, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 2, frUp, 1, 2, frAdd, 2, 0, frDown, 2, 1, frRemove, 0, 0, frSync, 0, 0,
		frRemove, 1, 0, frAdd, 0, 0, frSync, 0, 0, frUp, 2, 1, frSync, 0, 0},
	// 2 slots, both final: C stands in on slot 0 holding east; A, back,
	// finds no free slot and reclaims it, which releases Space; B leaves,
	// and C, first in line, gets slot 1 with a Resync that presses Space
	{7, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 2, frUp, 0, 2, frDown, 1, 2, frUp, 1, 2, frAdd, 2, 0, frDown, 2, 1, frRemove, 0, 0, frSync, 0, 0,
		frAdd, 0, 0, frSync, 0, 0, frRemove, 1, 0, frSync, 0, 0, frUp, 2, 1, frSync, 0, 0},
	// 2 slots, both provisional: A leaves, so C, which waited, gets slot 0
	// after B got slot 1; D's press takes over slot 1, assigned longer ago,
	// not slot 0
	{7, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frRemove, 0, 0, frAdd, 3, 0, frSync, 0, 0, frDown, 3, 0, frUp, 3, 0, frSync, 0, 0, frDown, 1, 1, frSync, 0, 0},
	// 3 slots, all final, and D and E wait: A and C leave, so D gets slot
	// 0 and then E slot 2; B leaves, and A, back, takes slot 1 and rehomes
	// slot 0 to D; B, back, waits, and its press takes over slot 0, whose
	// hand-off is the oldest still
	{11, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frDown, 0, 3, frUp, 0, 3, frDown, 1, 3, frUp, 1, 3, frDown, 2, 3, frUp, 2, 3, frAdd, 3, 0, frAdd, 4, 0,
		frRemove, 0, 0, frRemove, 2, 0, frRemove, 1, 0, frAdd, 0, 0, frSync, 0, 0, frAdd, 1, 0, frDown, 1, 3, frUp, 1, 3, frSync, 0, 0},
	// 2 slots, both final, and C and D wait: A and B leave, so C gets slot
	// 0 and D slot 1; A, back, reclaims slot 0, a new assignment, so C's
	// press takes over slot 1
	{7, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 3, frUp, 0, 3, frDown, 1, 3, frUp, 1, 3, frAdd, 2, 0, frAdd, 3, 0, frRemove, 0, 0, frRemove, 1, 0,
		frAdd, 0, 0, frSync, 0, 0, frDown, 2, 3, frUp, 2, 3, frSync, 0, 0},
	// 3 slots, buffer 1: slot 1 stalls and falls behind while slot 0 maps
	// east and south; then a drop resets every slot, and slot 1, unstalled,
	// catches up
	{8, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frDown, 1, 1, frStall, 1, 0, frDown, 1, 2, frUp, 1, 2, frDown, 1, 0, frDown, 0, 1, frDown, 0, 0,
		frWait, 5, 0, frDrop, 0, 0, frDown, 0, 1, frDown, 1, 2, frUnstall, 1, 0, frSync, 0, 0, frUp, 0, 1, frSync, 0, 0},
	// 4 slots, 6 pads: two slots hold east across a Reload, a press takes
	// a slot over while the Reload is under way, pads leave and come back,
	// and a second Reload finds one east still held
	{15, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frAdd, 3, 0, frAdd, 4, 0, frAdd, 5, 0, frDown, 0, 1, frDown, 2, 1, frReload, 0, 0, frDown, 4, 0,
		frRemove, 1, 0, frSync, 0, 0, frUp, 0, 1, frDown, 0, 1, frRemove, 3, 0, frAdd, 1, 0, frSync, 0, 0, frReload, 0, 0, frUp, 2, 1, frSync, 0, 0},
	// identical gamepads on 2 slots: the third stands in for each twin
	// that leaves, and each takes its slot back under its number
	{4, frAdd, 0, 1, frAdd, 1, 1, frAdd, 2, 1, frRemove, 0, 0, frSync, 0, 0, frAdd, 0, 1, frRemove, 1, 0, frSync, 0, 0, frAdd, 1, 1, frSync, 0, 0},
	// 4 slots, buffer 1: slot 3 stalls while its pad holds east and moves
	// a stick, and slot 0 maps meanwhile
	{12, frAdd, 0, 0, frAdd, 1, 0, frAdd, 2, 0, frAdd, 3, 0, frDown, 3, 1, frStall, 3, 0, frDown, 3, 2, frUp, 3, 2, frDown, 3, 0, frAxis, 3, 0x40,
		frDown, 0, 1, frUp, 3, 1, frSync, 0, 0, frUp, 0, 1},
	// 2 slots, both holding east: the combo of a gamepad never connected
	// pauses both, and B's resumes both
	{4, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frDown, 1, 1, frSync, 0, 0, frCombo, 3, 0, frSync, 0, 0, frDown, 0, 0, frCombo, 1, 0,
		frUp, 1, 1, frDown, 1, 1, frSync, 0, 0, frUp, 0, 1, frUp, 1, 1},
	// 2 slots, buffer 1: slot 1 stalls holding east while the combo pauses
	// and resumes; slot 0 maps meanwhile
	{4, frAdd, 0, 0, frAdd, 1, 0, frDown, 1, 1, frStall, 1, 0, frDown, 1, 2, frUp, 1, 2, frCombo, 0, 0, frDown, 0, 1, frCombo, 0, 0, frUp, 0, 1,
		frDown, 0, 1, frUnstall, 1, 0, frSync, 0, 0, frUp, 1, 1, frDown, 1, 1, frSync, 0, 0},

	// A remap (IN-18): the held east and the tap go up at once, and a late
	// release changes nothing; then the press counts again
	{0, frAdd, 0, 0, frDown, 0, 1, frDown, 0, 0, frRemap, 0, 0, frSync, 0, 0, frUp, 0, 1, frSync, 0, 0, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1},
	// a remap while behind, and of a gamepad that waits and one not connected
	{0, frAdd, 0, 0, frAdd, 1, 0, frDown, 0, 1, frStall, 0, 0, frDown, 0, 2, frUp, 0, 2, frRemap, 0, 0, frRemap, 1, 0, frRemap, 2, 0, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0},
	// a remap while paused and across a Reload
	{0, frAdd, 0, 0, frDown, 0, 1, frCombo, 0, 0, frRemap, 0, 0, frReload, 0, 0, frSync, 0, 0, frCombo, 0, 0, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1},

	// The system's suspension (DMN-09).

	// a lock while east is held, and its end; then a quiet one, which the
	// Sync applies
	{0, frAdd, 0, 0, frDown, 0, 1, frSuspend, 1, 0, frSync, 0, 0, frSuspend, 0, 0, frSync, 0, 0, frSuspend, 1, 1, frSync, 0, 0, frUp, 0, 1},
	// a quiet lock and its end before the router looks, with a tap and east
	// pressed meanwhile: all canceled, east until pressed again
	{0, frAdd, 0, 0, frDown, 0, 1, frSuspend, 1, 1, frDown, 0, 0, frUp, 0, 0, frDown, 0, 2, frSuspend, 0, 1, frSync, 0, 0,
		frUp, 0, 1, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1},
	// 2 slots, buffer 1: slot 1 stalls holding east through a lock, the
	// combo, a second reason and the end; the pause outlasts the lock
	{4, frAdd, 0, 0, frAdd, 1, 0, frDown, 1, 1, frStall, 1, 0, frDown, 1, 2, frUp, 1, 2, frSuspend, 1, 0, frCombo, 0, 0, frSuspend, 5, 0,
		frSuspend, 4, 0, frSuspend, 0, 0, frDown, 0, 1, frUnstall, 1, 0, frSync, 0, 0, frCombo, 0, 0, frSync, 0, 0, frUp, 1, 1, frDown, 1, 1, frSync, 0, 0},
	// sleep across a Reload, a removal and a reconnect, then every reason
	// and the end
	{0, frAdd, 0, 0, frDown, 0, 1, frSuspend, 8, 0, frReload, 0, 0, frRemove, 0, 0, frAdd, 0, 0, frDown, 0, 1, frSync, 0, 0,
		frSuspend, 0xff, 0, frSuspend, 0, 0, frSync, 0, 0, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0},
	// a stick deflected before a lock and centered during it, whose late
	// deflection is the first report after the resume: it is masked, and
	// stays so until it is centered and deflected again
	{0, frAdd, 0, 0, frAxis, 0, 0x40, frSuspend, 1, 0, frAxis, 0, 0, frSuspend, 0, 0, frAxis, 0, 0x40, frSync, 0, 0,
		frAxis, 0, 0, frAxis, 0, 0x40, frSync, 0, 0},
	// the same, but the first report after the resume is neutral: the center
	// was seen during the lock, so it does not end the quarantine, and a
	// deflection must be seen before a neutral report does (a stick, then a
	// trigger). Only these tell the router's needActive from its absence: a
	// deflection as the first report clears it anyway.
	{0, frAdd, 0, 0, frAxis, 0, 0x40, frSuspend, 1, 0, frAxis, 0, 0, frSuspend, 0, 0, frAxis, 0, 0, frAxis, 0, 0x40, frSync, 0, 0,
		frAxis, 0, 0, frAxis, 0, 0x40, frSync, 0, 0},
	{0, frAdd, 0, 0, frAxis, 3, 0x7f, frSuspend, 1, 0, frAxis, 3, 0, frSuspend, 0, 0, frAxis, 3, 0, frAxis, 3, 0x7f, frSync, 0, 0,
		frAxis, 3, 0, frAxis, 3, 0x7f, frSync, 0, 0},
	// a gamepad that connects during a lock, with a stick and a trigger moved
	// then: both are masked after the resume until they are centered
	{0, frSuspend, 1, 0, frAdd, 0, 0, frAxis, 0, 0x40, frAxis, 3, 0x7f, frSuspend, 0, 0, frAxis, 0, 0x50, frAxis, 3, 0, frSync, 0, 0,
		frAxis, 3, 0x7f, frAxis, 0, 0, frAxis, 0, 0x50, frSync, 0, 0},

	// A remap leaves the suspension's quarantine as it is (IN-18, ADR-0033).
	// The router's reset of a gamepad (remapped) clears its state and the
	// resume sample, not d.recovery, and only these tell that from a reset that
	// clears the quarantine too: the late report of a control that was held
	// through the lock is the first one after the resume.

	// east held through a lock and a remap during it: its late press after the
	// resume is masked, its release ends the quarantine, and the next press
	// counts
	{0, frAdd, 0, 0, frDown, 0, 1, frSuspend, 1, 0, frRemap, 0, 0, frSuspend, 0, 0, frDown, 0, 1, frSync, 0, 0,
		frUp, 0, 1, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1, frSync, 0, 0},
	// the same, with the remap after the resume and before the late press
	{0, frAdd, 0, 0, frDown, 0, 1, frSuspend, 1, 0, frSuspend, 0, 0, frRemap, 0, 0, frDown, 0, 1, frSync, 0, 0,
		frUp, 0, 1, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1, frSync, 0, 0},
	// east released by the lock (not by the owner) before the remap: needDown
	// survives it, so the late release does not end the quarantine, and a press
	// has to come first (this also fixes needDown of a release during a lock)
	{0, frAdd, 0, 0, frDown, 0, 1, frSuspend, 1, 0, frUp, 0, 1, frRemap, 0, 0, frSuspend, 0, 0, frUp, 0, 1, frDown, 0, 1, frSync, 0, 0,
		frUp, 0, 1, frDown, 0, 1, frSync, 0, 0, frUp, 0, 1, frSync, 0, 0},
	// a stick deflected through a lock and a remap during it: the late
	// deflection stays masked until the stick is centered and deflected again
	{0, frAdd, 0, 0, frAxis, 0, 0x40, frSuspend, 1, 0, frRemap, 0, 0, frSuspend, 0, 0, frAxis, 0, 0x40, frSync, 0, 0,
		frAxis, 0, 0, frAxis, 0, 0x40, frSync, 0, 0},
}

// FuzzRouter drives a Supervisor of 1 to MaxSlots slots with the events of
// two more gamepads than slots that leave and reconnect, stalls of the slot
// loops with small edge buffers, and drops of input. At every Sync it
// checks the router against a model of IN-05 and IN-08 (see the package
// doc): a gamepad that connects takes the free slot of the lowest index,
// if any, and becomes its home, rehoming a provisional stand-in on a slot
// whose home is its key; with no slot free it takes that slot back, and
// otherwise waits. A freed slot goes to the first in line, and the home
// stays. Each assignment is provisional until the holder's first press,
// and the press of a gamepad that waits takes over the provisional slot
// assigned longest ago, for good. A holder that loses its slot waits
// again, first. Each slot must have the model's gamepad, home and
// provisional flag, its state must be the router's, and the router's the
// model's; the Status must keep the router's invariants. Reloads (PRO-03)
// change none of this: the check waits for them, and then each slot and
// every key held must be of the last generation and its program. The
// ComboHeld events of any gamepad toggle the pause (DMN-06, DMN-07):
// Status.Paused must be the model's, and nothing is held while paused.
// Suspend and SuspendQuiet set the system's suspension (DMN-09), which
// holds the mapping too: Status.Suspended must be the model's, Output's
// system gate closed just while suspended and its user gate just while
// paused, and nothing is held while suspended. A stick or trigger that is
// deflected at the start of a suspension or moves during it is masked in the
// router's state after the resume, as a button is (ADR-0033), until the
// report that ends its quarantine (axisApplies). A DeviceRemapped (IN-18) of
// a connected gamepad resets it, as a drop does every one: its controls count
// as up in the router and in its slot, and one that is still held counts
// again after a release and a press; it leaves the suspension's quarantine
// as it is. A control held since a Reload, or since or across a pause or a suspension, holds no key, and a
// free slot holds none. The key of east, a hold, is held by just the
// slots whose gamepad holds east, but for those that may or may not have
// canceled it (see frSlot), and once for several, with each as an owner;
// the taps of south end on the clock, so their keys are only checked one
// way. At the end the harness stops Run and the output loop, and checks
// that nothing is held.
func FuzzRouter(f *testing.F) {
	for _, seed := range frSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		fz := newRouterFuzzer(t, 1+int(data[0]%4), 1+int(data[0]/4%4))
		data = data[1:]
		if len(data) > 3*128 {
			data = data[:3*128]
		}
		for i := 0; i < len(data); i += 3 {
			var arg [2]byte
			copy(arg[:], data[i+1:min(i+3, len(data))])
			fz.step(data[i]%frOps, arg[0], arg[1])
		}
		fz.check()
	})
}

// routerFuzzer is FuzzRouter's harness and model.
type routerFuzzer struct {
	t       *testing.T
	h       *harness
	dropped atomic.Uint64
	gate    [supervisor.MaxSlots]atomic.Pointer[chan struct{}] // non-nil while the slot stalls
	// known is the device of the last DeviceAdded the slot saw, 0 after
	// its DeviceRemoved; stray is set when the slot saw a DeviceAdded
	// while it knew a device, or a DeviceRemoved of another one.
	known  [supervisor.MaxSlots]atomic.Uint32
	stray  [supervisor.MaxSlots]atomic.Bool
	pads   []frPad // slots+2 of them
	nextID input.DeviceID
	// The model of the router: reg gives the pads their keys, as the
	// router's Registry does; slots are the slots, by index; queue holds
	// the pads that wait, in order; seq counts the assignments.
	reg   *input.Registry
	slots []frSlot
	queue []int
	seq   uint64
	// The model of Reload: gen is the last generation, progs the programs
	// of the odd and the even ones, and waits the Reloads to wait for;
	// pending is set from a Reload until the check waits for it.
	gen     uint32
	progs   [2]*engine.Program
	waits   []func(context.Context) error
	pending bool
	// The model of the hold: paused is the user's pause, and suspended the
	// reasons of the system's suspension last set, which the router applies
	// by the next check, also those of SuspendQuiet; toggled is set from a
	// ComboHeld or a change of the reasons until the next check.
	paused, toggled bool
	suspended       winsession.Reason
}

// frSlot is the model of a slot.
type frSlot struct {
	pad         int // assigned, or -1 while free
	home        input.DeviceKey
	provisional bool
	seq         uint64 // the seq of its current assignment
	// Of the controls of frKeys that the slot's pad, stalePad, still holds,
	// stale are those it held at a Reload or while paused or suspended:
	// canceled, they do nothing. unsure are those the slot may have canceled
	// or not: pressed, or held when it got the slot, while a Reload was
	// pending, as the slot may have handled the press before the Reload or
	// after, or after a resume or a change of the suspension before the next
	// check, as a slot behind across a pause and a resume cancels what its
	// Resync brings in, and the router applies a suspension when it looks;
	// or pressed again after a release in released. released holds the stale and unsure controls
	// released since the last Sync: a slot behind may miss the release and
	// the next press, as its Resync sees the control held all along, and the
	// control stays canceled.
	stale, unsure, released map[control.Control]bool
	stalePad                int
}

// forget clears the controls of the slot's last pad, for its current one.
func (s *frSlot) forget() {
	s.stale, s.unsure, s.released = map[control.Control]bool{}, map[control.Control]bool{}, map[control.Control]bool{}
	s.stalePad = s.pad
}

// frKeys are the keys of the controls bound in the programs of odd and
// even generations, program and program2: east holds its key, and south
// taps its keys for 20 ms.
var frKeys = [2]map[control.Control][]string{
	{control.East: {"Space"}, control.South: {"LCtrl", "Z"}},
	{control.East: {"LShift"}, control.South: {"X"}},
}

// frPad is the model of a gamepad.
type frPad struct {
	id        input.DeviceID    // of its last connection; 0 before it connected
	info      *input.DeviceInfo // of its last connection
	key       input.DeviceKey   // of its last connection
	connected bool
	state     slot.State
	// System holds can report artificial ups. These controls stay
	// quarantined across handoffs until a resumed release is established.
	quarantine [control.Count]bool
	needDown   [control.Count]bool
	// The axes have the same quarantine (ADR-0033): one that is deflected at
	// the start of a suspension, or moves during it, is masked after the
	// resume until a neutral report ends it, and needActive is set when
	// the suspension saw it go neutral, so that it must be deflected again
	// first. A stick is one group, as in the router.
	axQuarantine [control.AxisCount]bool
	axNeedActive [control.AxisCount]bool
}

// frAxisGroup returns the axes that are quarantined together with a.
func frAxisGroup(a control.Axis) []control.Axis {
	switch a {
	case control.LeftX, control.LeftY:
		return []control.Axis{control.LeftX, control.LeftY}
	case control.RightX, control.RightY:
		return []control.Axis{control.RightX, control.RightY}
	}
	return []control.Axis{a}
}

// frNeutral reports whether a at v is neutral: inside the stick's radial
// deadzone, or below the trigger's release threshold, of the default analog
// settings the fuzz programs have. FuzzRouter drives leftx and both triggers
// only, so the other axis of the stick, which the radius counts, is 0.
func frNeutral(a control.Axis, v int16) bool {
	analog := profile.DefaultAnalog()
	x := float64(v) / 32767
	switch a {
	case control.LeftX, control.LeftY:
		return math.Abs(x) <= analog.LS.Deadzone
	case control.RightX, control.RightY:
		return math.Abs(x) <= analog.RS.Deadzone
	case control.LeftTrigger:
		return x <= analog.LT.Release
	case control.RightTrigger:
		return x <= analog.RT.Release
	}
	return true
}

// axisApplies applies the router's quarantine of axes to a report of a at v
// from p and says whether p's state takes it: a report the router swallows
// leaves the state as the resume masked it.
func (fz *routerFuzzer) axisApplies(p *frPad, a control.Axis, v int16) bool {
	suspended := fz.suspended != 0
	if !suspended && !p.axQuarantine[a] {
		return true
	}
	neutral := frNeutral(a, v)
	group := frAxisGroup(a)
	if suspended && !neutral {
		for _, g := range group {
			p.axQuarantine[g] = true
		}
	}
	if !p.axQuarantine[a] {
		return true
	}
	switch {
	case suspended:
		for _, g := range group {
			p.axNeedActive[g] = neutral
		}
		return true
	case !neutral:
		for _, g := range group {
			p.axNeedActive[g] = false
		}
	case !p.axNeedActive[a]:
		for _, g := range group {
			p.axQuarantine[g] = false
		}
		return true
	}
	return false
}

func newRouterFuzzer(t *testing.T, buffer, slots int) *routerFuzzer {
	fz := &routerFuzzer{
		t: t, reg: input.NewRegistry(), pads: make([]frPad, slots+2),
		gen: 1, progs: [2]*engine.Program{compile(t), compile2(t)},
	}
	for range slots {
		fz.slots = append(fz.slots, frSlot{pad: -1, stalePad: -1})
	}
	fz.h = start(t, opts{slots: slots, buffer: buffer, dropped: fz.dropped.Load, onEvent: fz.onEvent, quiet: true})
	t.Cleanup(fz.unstallAll) // before the harness stops Run
	return fz
}

// onEvent blocks slot i's loop while it stalls, and follows the device the
// slot knows.
func (fz *routerFuzzer) onEvent(i uint8, e input.Event) {
	if g := fz.gate[i].Load(); g != nil {
		<-*g
	}
	switch e.Kind {
	case input.DeviceAdded:
		if fz.known[i].Swap(uint32(e.Device)) != 0 {
			fz.stray[i].Store(true)
		}
	case input.DeviceRemoved:
		if !fz.known[i].CompareAndSwap(uint32(e.Device), 0) {
			fz.stray[i].Store(true)
		}
	}
}

func (fz *routerFuzzer) unstall(i int) {
	if g := fz.gate[i].Swap(nil); g != nil {
		close(*g)
	}
}

func (fz *routerFuzzer) unstallAll() {
	for i := range fz.gate {
		fz.unstall(i)
	}
}

func (fz *routerFuzzer) step(op, a, b byte) {
	defer fz.follow()
	k := int(a) % len(fz.pads)
	p := &fz.pads[k]
	switch op {
	case frAdd:
		if !p.connected {
			// Pad k's own info, or the twins' when b%2 is 1. The same info
			// as its last connection is the same key, and the Registry
			// reports a reconnect; the twins have the same info, so the
			// Registry numbers them, and they may swap numbers when they
			// reconnect.
			name, guid := frNames[k], frGUIDs[k]
			if b%2 == 1 {
				name, guid = "Pad", "0c0c"
			}
			fz.nextID++
			info := pad(fz.nextID, name, guid)
			key, _ := fz.reg.Connect(info)
			*p = frPad{id: fz.nextID, info: info, key: key, connected: true}
			fz.connect(k)
		}
		info := *p.info
		fz.h.send(added(&info))
	case frRemove:
		if p.connected {
			p.connected = false
			fz.reg.Disconnect(p.id)
			fz.disconnect(k)
		}
		fz.h.send(removed(p.id))
	case frDown, frUp:
		c, _ := control.Parse(frButtons[int(b)%len(frButtons)])
		if op == frDown && p.connected && (fz.suspended != 0 || !p.quarantine[c]) {
			fz.press(k)
		}
		fz.event(p, button(p.id, frButtons[int(b)%len(frButtons)], op == frDown))
	case frAxis:
		fz.event(p, axis(p.id, frAxes[int(a)/len(fz.pads)%len(frAxes)], int16(b)<<8))
	case frStall:
		if i := int(a) % len(fz.slots); fz.gate[i].Load() == nil {
			g := make(chan struct{})
			fz.gate[i].Store(&g)
		}
	case frUnstall:
		fz.unstall(int(a) % len(fz.slots))
	case frDrop:
		fz.dropped.Add(uint64(a%3) + 1)
		// The Sync sees the drop, if a retry did not: either way the reset
		// comes after every event sent so far.
		for i := range fz.pads {
			fz.pads[i].state = slot.State{}
		}
		fz.check()
	case frSync:
		fz.check()
	case frWait:
		fz.h.clk.Advance(time.Duration(a) * ms)
	case frReload:
		fz.reload()
	case frCombo:
		// The router toggles the pause for any id, even one it does not know.
		fz.paused, fz.toggled = !fz.paused, true
		fz.h.send(input.Event{Device: p.id, Kind: input.ComboHeld})
	case frRemap:
		// Like a drop, for one gamepad: its controls count as up, in the router and
		// in its slot, and a slot behind catches up to that. The router's quarantine
		// of the suspension (ADR-0033) is not touched by it, nor the model's: a
		// control held through a suspension stays masked until it was released and
		// pressed, and an axis until it was neutral and deflected again.
		if p.connected {
			p.state = slot.State{}
		}
		ev := input.Event{Device: p.id, Kind: input.DeviceRemapped}
		if p.info != nil { // a gamepad that never connected has none
			info := *p.info
			ev.Info = &info
		}
		fz.h.send(ev)
	case frSuspend:
		// Suspend is not in the order of the events: the router reads the
		// suspension when it routes an event, so one it took before the call
		// and had not routed yet would count as sent during the suspension,
		// and the model's order of events and suspensions would not be the
		// router's. send returns when the router took the event, which it
		// does after routing the one before (an unknown gamepad's removal is
		// no event of the model), so that all earlier events are routed.
		fz.h.send(removed(0))
		r := winsession.Reason(a) & winsession.AllReasons
		if r&^fz.suspended != 0 {
			for i := range fz.pads {
				pad := &fz.pads[i]
				for c, down := range pad.state.Buttons {
					pad.quarantine[c] = pad.quarantine[c] || down
				}
				for _, a := range control.AllAxes() {
					if !frNeutral(a, pad.state.Axes[a]) {
						for _, g := range frAxisGroup(a) {
							pad.axQuarantine[g] = true
						}
					}
				}
			}
		}
		if r == 0 {
			for i := range fz.pads {
				pad := &fz.pads[i]
				for c, blocked := range pad.quarantine {
					if blocked {
						pad.state.Buttons[c] = false
					}
				}
				for a, blocked := range pad.axQuarantine {
					if blocked {
						pad.state.Axes[a] = 0
					}
				}
			}
		}
		if r != fz.suspended {
			fz.suspended, fz.toggled = r, true
		}
		if b%2 == 1 {
			fz.h.sup.SuspendQuiet(winsession.Reason(a))
		} else {
			fz.h.sup.Suspend(winsession.Reason(a))
		}
		if r == 0 {
			// A down racing the resume drain may either be quarantined
			// or a fresh press. Establish the model's release boundary;
			// queued/collapsed resume timing has dedicated golden tests.
			fz.check()
		}
	}
}

// reload starts a Reload of the next program; check waits for it. The
// controls each slot's pad holds become stale.
func (fz *routerFuzzer) reload() {
	t := fz.t
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	gen, wait, err := fz.h.sup.StartReload(ctx, fz.progs[fz.gen%2])
	if err != nil || gen != fz.gen+1 {
		t.Fatalf("StartReload = %d, %v; want %d, nil", gen, err, fz.gen+1)
	}
	fz.gen, fz.pending = gen, true
	fz.waits = append(fz.waits, wait)
	for i := range fz.slots {
		s := &fz.slots[i]
		s.forget()
		if s.pad >= 0 {
			for c := range frKeys[0] {
				s.stale[c] = fz.pads[s.pad].state.Buttons[c]
			}
		}
	}
}

// follow keeps stale and unsure to the controls each slot's pad still
// holds: one released, or reset by a drop, is pressed again with the
// program, but for a slot that missed the release; another pad in the
// slot holds nothing stale. While paused or suspended, each control held
// is stale: a suspension the router has not applied yet cancels it once it
// does. A control that the pad holds while a Reload is pending, and not
// since before it, or since a toggle of the pause or a change of the
// suspension, or holds again after a release in released, is unsure.
func (fz *routerFuzzer) follow() {
	for i := range fz.slots {
		s := &fz.slots[i]
		if s.pad != s.stalePad || s.stale == nil {
			s.forget()
		}
		var held [control.Count]bool
		if s.pad >= 0 {
			held = fz.pads[s.pad].state.Buttons
		}
		for c := range frKeys[0] {
			if !held[c] && (s.stale[c] || s.unsure[c]) {
				s.released[c] = true
			}
			s.stale[c] = held[c] && (s.stale[c] || fz.paused || fz.suspended != 0)
			s.unsure[c] = held[c] && !s.stale[c] && (s.unsure[c] || s.released[c] || fz.pending || fz.toggled)
		}
	}
}

// slotOf returns the slot of pad k, or -1.
func (fz *routerFuzzer) slotOf(k int) int {
	return slices.IndexFunc(fz.slots, func(s frSlot) bool { return s.pad == k })
}

// assign gives slot i to pad k: k becomes the home of a slot that has none
// and, unless provisional, of any.
func (fz *routerFuzzer) assign(i, k int, provisional bool) {
	s := &fz.slots[i]
	fz.seq++
	s.pad, s.provisional, s.seq = k, provisional, fz.seq
	if !provisional || s.home == (input.DeviceKey{}) {
		s.home = fz.pads[k].key
	}
}

// connect applies the new connection of pad k to the model: it takes the
// free slot of the lowest index, provisionally, and a provisional stand-in
// on the slot whose home is its key makes that slot its own home; with no
// slot free, it takes that slot back, provisionally, and the stand-in
// waits first; otherwise it waits.
func (fz *routerFuzzer) connect(k int) {
	key := fz.pads[k].key
	home := slices.IndexFunc(fz.slots, func(s frSlot) bool {
		return s.provisional && s.home == key && s.pad >= 0 && fz.pads[s.pad].key != key
	})
	free := fz.slotOf(-1)
	switch {
	case free >= 0:
		if home >= 0 {
			s := &fz.slots[home]
			s.home = fz.pads[s.pad].key
		}
		fz.assign(free, k, true)
	case home >= 0:
		fz.queue = slices.Insert(fz.queue, 0, fz.slots[home].pad)
		fz.assign(home, k, true)
	default:
		fz.queue = append(fz.queue, k)
	}
}

// disconnect applies the removal of pad k to the model: it stops waiting,
// or hands its slot on to the first in line, or frees the slot.
func (fz *routerFuzzer) disconnect(k int) {
	if slices.Contains(fz.queue, k) {
		fz.queue = slices.DeleteFunc(fz.queue, func(q int) bool { return q == k })
		return
	}
	i := fz.slotOf(k)
	switch {
	case i < 0:
	case len(fz.queue) == 0:
		fz.slots[i].pad, fz.slots[i].home, fz.slots[i].provisional = -1, input.DeviceKey{}, false
	default:
		next := fz.queue[0]
		fz.queue = fz.queue[1:]
		fz.assign(i, next, true)
	}
}

// press applies a press of pad k to the model, before its state: the pad
// that holds a provisional slot confirms it; a pad that waits takes over
// the provisional slot assigned longest ago, and the holder waits again,
// first.
func (fz *routerFuzzer) press(k int) {
	if i := fz.slotOf(k); i >= 0 {
		s := &fz.slots[i]
		s.home, s.provisional = fz.pads[k].key, false
		return
	}
	if !slices.Contains(fz.queue, k) {
		return
	}
	oldest := -1
	for i, s := range fz.slots {
		if s.provisional && (oldest < 0 || s.seq < fz.slots[oldest].seq) {
			oldest = i
		}
	}
	if oldest < 0 {
		return
	}
	fz.queue = slices.DeleteFunc(fz.queue, func(q int) bool { return q == k })
	fz.queue = slices.Insert(fz.queue, 0, fz.slots[oldest].pad)
	fz.assign(oldest, k, false)
}

// event sends e of p, and applies it to the model while p is connected.
func (fz *routerFuzzer) event(p *frPad, e input.Event) {
	if p.connected {
		apply := true
		if c, ok := control.Parse(e.Control); ok && (e.Kind == input.ButtonDown || e.Kind == input.ButtonUp) {
			if fz.suspended != 0 {
				if e.Kind == input.ButtonDown {
					p.quarantine[c], p.needDown[c] = true, false
				} else if p.quarantine[c] {
					p.needDown[c] = true
				}
			} else if p.quarantine[c] {
				apply = false
				if e.Kind == input.ButtonDown {
					p.needDown[c] = false
				} else if !p.needDown[c] {
					p.quarantine[c], apply = false, true
				}
			}
		}
		if a, ok := control.ParseAxis(e.Control); ok && e.Kind == input.AxisMotion {
			apply = fz.axisApplies(p, a, e.Value)
		}
		if apply {
			p.state.Apply(e)
		}
	}
	fz.h.send(e)
}

// check syncs and checks the slots and the router against the model.
func (fz *routerFuzzer) check() {
	t := fz.t
	t.Helper()
	fz.follow()
	fz.unstallAll()
	for _, wait := range fz.waits {
		fz.h.ticking("Reload", wait)
	}
	fz.waits, fz.pending, fz.toggled = nil, false, false
	states := fz.h.statesTicking()
	if len(states) != len(fz.slots) {
		t.Fatalf("%d Sync states, want %d", len(states), len(fz.slots))
	}
	// Each slot has caught up, so it has seen each release: a control
	// released now is canceled no more.
	for i := range fz.slots {
		clear(fz.slots[i].released)
	}
	all := fz.h.sup.Status()
	if all.Paused != fz.paused || all.Suspended != fz.suspended {
		t.Fatalf("Status paused %v, suspended %v; model %v, %v", all.Paused, all.Suspended, fz.paused, fz.suspended)
	}
	// The Sync returned after the router opened the system gate, if it did
	// (openAfter).
	if got := fz.h.out.Blocking(); (got&output.BlockSystem != 0) != (fz.suspended != 0) || (got&output.BlockUser != 0) != fz.paused {
		t.Fatalf("Output.Blocking() %v, model paused %v, suspended %v", got, fz.paused, fz.suspended)
	}
	status := all.Slots
	if len(status) != len(fz.slots) {
		t.Fatalf("Status has %d slots, want %d", len(status), len(fz.slots))
	}
	for i, s := range fz.slots {
		st := states[i]
		if !st.Ran {
			t.Fatalf("slot %d did not run the Sync", i)
		}
		if st.Got != st.Want {
			t.Fatalf("slot %d state %+v, router state %+v", i, st.Got, st.Want)
		}
		want := supervisor.SlotStatus{Index: uint8(i), Gen: fz.gen, Home: s.home, Provisional: s.provisional}
		if s.pad >= 0 {
			p := fz.pads[s.pad]
			want.Key, want.ID, want.Name = p.key, p.id, p.info.Name
			if st.Want != p.state {
				t.Fatalf("slot %d: router state %+v, model %+v", i, st.Want, p.state)
			}
		}
		if st.Assigned != (s.pad >= 0) {
			t.Fatalf("slot %d: Sync assigned %v, model has pad %d", i, st.Assigned, s.pad)
		}
		if got := status[i]; got != want {
			t.Fatalf("slot %d status %+v, model %+v (queue %v)", i, got, want, fz.queue)
		}
		// No slot is ever disabled here, so DeviceAdded and DeviceRemoved
		// alternate on each, and the last DeviceAdded is of the gamepad
		// assigned now; one assigned and gone again while the slot was
		// behind may not appear.
		if fz.stray[i].Load() {
			t.Fatalf("slot %d saw a DeviceAdded or DeviceRemoved out of order", i)
		}
		if got := input.DeviceID(fz.known[i].Load()); got != want.ID {
			t.Fatalf("slot %d saw the DeviceAdded of device %d last, model %d", i, got, want.ID)
		}
	}
	var matching []input.DeviceKey
	for _, p := range fz.pads {
		if p.connected {
			matching = append(matching, p.key)
		}
	}
	if err := supervisor.CheckInvariants(status, matching, len(fz.queue)); err != nil {
		t.Fatalf("router invariants: %v\nstatus %+v", err, status)
	}
	fz.checkHeld()
}

// checkHeld checks the keys held after the Reloads: none while paused or
// suspended; each is of the last generation and of its program, owned by
// assigned slots, and a stale control holds none of its. The key of east
// is held by each slot whose pad holds east, neither stale nor unsure, and
// by no slot whose pad does not hold it or holds it stale; the taps of
// south end on the clock, so they are only checked one way.
func (fz *routerFuzzer) checkHeld() {
	t := fz.t
	t.Helper()
	var held []output.HeldKey
	if err := fz.h.out.Do(func(a *output.Arbiter) error {
		held = a.Held()
		return nil
	}); err != nil {
		t.Fatalf("output.Loop.Do: %v", err)
	}
	if (fz.paused || fz.suspended != 0) && len(held) > 0 {
		t.Fatalf("paused %v, suspended %v, but %v held", fz.paused, fz.suspended, held)
	}
	prog := frKeys[(fz.gen-1)%2]
	keys := map[string]control.Control{}
	for c, ks := range prog {
		for _, k := range ks {
			keys[k] = c
		}
	}
	// east's key, by the slots that hold it for sure and may hold it.
	hold := prog[control.East][0]
	var must, may [supervisor.MaxSlots]bool
	for i, s := range fz.slots {
		if s.pad >= 0 && fz.pads[s.pad].state.Buttons[control.East] && !s.stale[control.East] {
			must[i], may[i] = !s.unsure[control.East], true
		}
	}
	var owns [supervisor.MaxSlots]bool
	for _, k := range held {
		name := k.Press.Key.String()
		c, ok := keys[name]
		if !ok {
			t.Fatalf("%s held, not a key of generation %d (%v)", name, fz.gen, held)
		}
		for _, o := range k.Owners {
			if int(o.Slot) >= len(fz.slots) || o.Gen != fz.gen {
				t.Fatalf("%s held by %v, with %d slots of generation %d", name, o, len(fz.slots), fz.gen)
			}
			s := fz.slots[o.Slot]
			switch {
			case s.pad < 0:
				t.Fatalf("%s held by %v, of a free slot", name, o)
			case s.stale[c]:
				t.Fatalf("%s held by %v, but %v is held since a Reload, a pause or a suspension", name, o, c)
			case name == hold && !may[o.Slot]:
				t.Fatalf("%s held by %v, whose pad does not hold %v", name, o, c)
			}
			if name == hold {
				owns[o.Slot] = true
			}
		}
	}
	for i := range fz.slots {
		if must[i] && !owns[i] {
			t.Fatalf("slot %d holds %v, but not %s (%v)", i, control.East, hold, held)
		}
	}
}
