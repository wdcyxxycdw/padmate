# ADR-0003: Several controllers, hot-plug and slot assignment

[中文](0003-devices-and-slots.zh-CN.md)

Related: IN-05, IN-08, IN-09, IN-21, IN-26, IN-27, PRO-11; [ARCHITECTURE §5](../ARCHITECTURE.md#5-threading-model), [§8.2](../ARCHITECTURE.md#82-output-arbiter); for the slot loop and the supervisor, see [ADR: Engine and threads](0005-engine-and-threads.md)

## Context

- Up to four controllers are mapped at once, one slot each; the rest wait in a queue. A disconnect must ReleaseAll, and a reconnect by DeviceKey must bring the mapping back.
- A lost connect or disconnect event is never sent again, and the router would no longer match the real devices.
- A machine often has an idle controller lying aside (one on a 2.4 GHz receiver, say, which is often enumerated first). If an empty slot simply went to the first controller that connects, that one would get it at startup; and when a controller dropped out briefly, someone else would take its slot.

## Decision

1. **The Source never drops device events.** When the channel is full, T-input keeps the DeviceAdded and DeviceRemoved events it could not send, in order, in a backlog. While the backlog is not empty, button and axis events are dropped and counted, and no event overtakes a queued device event. When a Removed meets an Added of the same id still in the backlog, both are taken out.
2. **Slots are built in advance.** The Supervisor builds 1–4 slots in New (padmated uses 4), controller or not. Each slot has its own engine, runner and edge channel, and the keys it presses carry its own `output.Owner.Slot`. A slot that is slow, stuck, panics or is disabled neither affects nor releases another slot.
3. **The Arbiter shares keys by reference count.** When two slots press the same key it goes down once and comes up when the last one lets go; modifiers stack at the system level. When events are lost, every slot is reset, and every controller's held keys are released.
4. **Assignment rules.** Each slot has a home (a DeviceKey) and a "standing in" flag. When a matching controller d connects:
   - If a slot is empty, d stands in for it and becomes its home.
   - If no slot is empty, and the slot whose home is d is held by a stand-in, d takes it back and the stand-in goes to the front of the queue.
   - Otherwise d goes to the back of the queue.
   - When a slot's controller leaves, the slot is handed at once to the front of the queue (its home stays). With nobody waiting it is idle, its home is cleared, and an empty slot is kept for no key.
5. **Standing in becomes final on a button press.** Taking an empty slot, a handover and taking a slot back all start as standing in. The stand-in's first ButtonDown (any button) makes the assignment final, and the home becomes it. Axis events do not count, because an idle controller's sticks jitter; buttons already held at the handover do not count either.
6. **Taking over.** A queued controller's ButtonDown takes over the stand-in slot that was assigned earliest (whoever its home is), and that stand-in goes to the front of the queue. So an idle controller that connected first cannot block the controller in use. Axis events cannot take over, and neither can triggers.
7. **Resync on handover.** The new controller's held buttons count as pressed, and its triggers and sticks take their current values, the same as for a controller that connects with a button held.
8. **A slot that falls behind loses no assignment.** While a slot is behind, its pending edges are an ordered list of at most two: first the release, then the DeviceAdded of the controller assigned now. A removal that meets a pending Added of the same controller cancels both. Once it catches up, they are delivered in order and then a Resync follows, so the slot still sees Added and Removed alternate. When a slot is disabled, the dead slot's edges are dropped, and if the controller assigned now has an Added, the slot is enabled again at once.
9. **A disabled slot keeps its controller.** It is not moved to an empty slot, so that the event that made one slot panic does not break the next.
10. **Reconnects.** A reconnected controller that gets a slot is mapped with the current Program; the keys it held before have been released, and its layer state is restored by DeviceKey. Reconnecting with a button held depends on the backend (rawinput/hidapi report it pressed, xinput does not); the engine handles both.

## Costs

- One press on an idle controller while it stands in gives it the slot, and the controller in hand has to queue. Use `--device` to pin a controller, or IN-21 to ignore a device.
- An idle controller takes a slot too, and its buttons and axes are mapped by the profile.
- A controller used only for sticks or triggers stays a stand-in; taking over needs a button press.
- Two identical controllers differ only in #N in their keys. While the home controller is away, the other identical one may get its #N and take the slot back.
- There are four locked threads even with one controller; more controllers make lost events more likely, and a lost event resets every controller.

## Alternatives given up

- **First in, first out, with no taking back.** A controller that dropped out briefly would have to queue when it came back, and an idle controller would hold its slot for good.
- **The home takes the slot back at any time, pressed or not.** The controller in use would lose its slot in the middle of a press.
- **Taking an empty slot is final at once.** The idle controller that connects first at startup would keep it.
- **Take back first, then fill an empty slot.** The stand-in's keys would go up and down again, cutting a held key.
- **Axis events, or buttons held at the handover, also confirm.** A jittering stick would keep an idle controller in its slot.
- **Device events on a channel of their own.** Two channels have no order between them.
- **Build slots on demand, a goroutine per controller.** The runner, the barrier and Status would all have to handle slots coming and going, while four idle slots cost very little.
- **Each slot arbitrates modifiers on its own.** The system has one keyboard state; the timing gets complicated, and a held combination could be cut.
