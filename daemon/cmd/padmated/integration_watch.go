package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspfile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration/cspint"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// The software integrations in the running daemon (ARCHITECTURE section 16):
// the registry, the watcher that follows the software of the active profile,
// and the check when a profile that is bound to a piece of software becomes
// active.
//
// The watcher polls (a stat of the software's files and a process snapshot)
// every integrationPollEvery while the active profile is bound to an
// integration or some client subscribes to integration.changed. Otherwise it
// only looks at the in-memory rules, and does no I/O at all.

// integrationTopic is the topic of integration.changed.
const integrationTopic = "integration.changed"

// integrationPollEvery is how often the watcher looks at the software.
const integrationPollEvery = 2 * time.Second

// defaultUILanguage is the language of the texts the daemon words itself
// (the notices) until a request has told another one.
const defaultUILanguage = "zh-CN"

// rpcIntegrationChanged is the data of integration.changed.
type rpcIntegrationChanged struct {
	Integration  string `json:"integration"`
	Kind         string `json:"kind"`
	Profile      string `json:"profile,omitempty"`
	DifferenceID string `json:"differenceId,omitempty"`
	Count        int    `json:"count,omitempty"`
}

// integrationWatch is what the watcher remembers of one integration.
type integrationWatch struct {
	// profile is the active profile the last tick found bound to it.
	profile string
	rev     string
	haveRev bool
	running bool
	haveRun bool
}

// integrationRun is the integrations part of a liveRun.
type integrationRun struct {
	// reg is set before the watcher starts and never changes; nil or empty
	// means no integrations.
	reg *integration.Registry
	// lang is the language of the last request that named one.
	lang atomic.Pointer[string]
	// kick asks the watcher to look now (capacity 1); recheck makes it run
	// the check of the active profile even if nothing changed.
	kick    chan struct{}
	recheck atomic.Bool
	// notify shows a notice in the tray; nil means the tray's queue when
	// there is a tray.
	notify func(tray.Notification)
	// every is the polling interval; zero means integrationPollEvery.
	every time.Duration

	stop context.CancelFunc
	done chan struct{}

	mu sync.Mutex
	// announced is the difference ID last announced for profile+integration
	// in this run.
	announced map[string]string
	// logged is the error last logged for an integration, so that it is
	// logged once.
	logged map[string]string
	watch  map[string]*integrationWatch
}

// newDefaultIntegrations returns the registry of production: CSP, with its
// data folder from the live config and its names from the installation.
func (r *liveRun) newDefaultIntegrations(padmateDir string) *integration.Registry {
	reg := integration.NewRegistry()
	var once sync.Once
	var install string
	installDir := func() (string, error) {
		var err error
		once.Do(func() {
			var d cspfile.InstallDir
			if d, err = cspfile.FindInstallDir(); err == nil {
				install = d.Root
			}
		})
		if install == "" {
			return "", fmt.Errorf("CLIP STUDIO PAINT's installation not found")
		}
		return install, nil
	}
	reg.Register(cspint.New(cspint.Options{
		PadmateDir: padmateDir,
		DataDir:    func() string { return r.selectionConfig().Integrations[cspint.ID].DataDir },
		Namer:      &cspint.NamesNamer{InstallDir: installDir},
	}))
	return reg
}

// integrations returns the registry, never nil.
func (r *liveRun) integrations() *integration.Registry {
	if r.integ.reg == nil {
		return integration.NewRegistry()
	}
	return r.integ.reg
}

// integLang resolves the language of a request: the one it names, else the
// last one a request named, else Chinese. A language that is named is
// remembered.
func (r *liveRun) integLang(requested string) string {
	if requested != "" {
		lang := normalizeLanguage(requested)
		r.integ.lang.Store(&lang)
		return lang
	}
	if p := r.integ.lang.Load(); p != nil {
		return *p
	}
	return defaultUILanguage
}

// normalizeLanguage maps a language tag to "zh-CN" or "en".
func normalizeLanguage(tag string) string {
	if len(tag) >= 2 && (tag[:2] == "zh" || tag[:2] == "ZH" || tag[:2] == "Zh") {
		return "zh-CN"
	}
	return "en"
}

// integKick asks the watcher to look now. It never blocks.
func (r *liveRun) integKick() {
	select {
	case r.integ.kick <- struct{}{}:
	default:
	}
}

// startIntegrations starts the watcher when there are integrations.
func (r *liveRun) startIntegrations() {
	if len(r.integrations().All()) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(r.end)
	i := &r.integ
	i.stop, i.done = cancel, make(chan struct{})
	go func() {
		defer close(i.done)
		defer func() {
			if v := recover(); v != nil {
				r.log.Error("integration watcher stopped", "panic", v)
			}
		}()
		every := i.every
		if every <= 0 {
			every = integrationPollEvery
		}
		for ctx.Err() == nil {
			r.integrationTick()
			t := time.NewTimer(every)
			select {
			case <-ctx.Done():
			case <-i.kick:
			case <-t.C:
			}
			t.Stop()
		}
	}()
}

// stopIntegrations stops the watcher and waits for it, a second at most.
func (r *liveRun) stopIntegrations() {
	i := &r.integ
	if i.stop == nil {
		return
	}
	i.stop()
	select {
	case <-i.done:
	case <-time.After(watchStopWait):
		r.log.Debug("integration watcher did not stop before shutdown")
	}
}

// activeProfileID is the ID of the profile running, "" without a library.
func (r *liveRun) activeProfileID() string {
	if p := r.profileTarget.Load(); p != nil {
		return p.id
	}
	return r.cfg.opts.profileID
}

// integrationTick is one look at every integration.
func (r *liveRun) integrationTick() {
	i := &r.integ
	rules := r.selectionConfig().Rules
	active := r.activeProfileID()
	listening := r.events.Listening(integrationTopic)
	force := i.recheck.Swap(false)
	for _, in := range r.integrations().All() {
		id := in.ID()
		bound := active != "" && integration.Bound(rules, active, in.Exes())
		i.mu.Lock()
		w := i.watch[id]
		if w == nil {
			w = &integrationWatch{}
			if i.watch == nil {
				i.watch = map[string]*integrationWatch{}
			}
			i.watch[id] = w
		}
		i.mu.Unlock()
		if !bound && !listening {
			*w = integrationWatch{}
			continue
		}

		if running, err := in.Running(); err == nil {
			if w.haveRun && running != w.running {
				r.publishIntegration(rpcIntegrationChanged{Integration: id, Kind: "running"})
			}
			w.running, w.haveRun = running, true
		}

		rev, err := in.Revision()
		if err != nil {
			r.integrationLogOnce(id, err)
			rev = ""
		}
		filesChanged := w.haveRev && rev != w.rev
		w.rev, w.haveRev = rev, true
		if filesChanged {
			r.publishIntegration(rpcIntegrationChanged{Integration: id, Kind: "files"})
		}

		if bound {
			if w.profile != active || filesChanged || force {
				r.integrationCheck(in, active)
			}
			w.profile = active
		} else {
			w.profile = ""
		}
	}
}

func (r *liveRun) publishIntegration(e rpcIntegrationChanged) {
	if r.events.Listening(integrationTopic) {
		r.events.Publish(integrationTopic, e)
	}
}

// integrationLogOnce logs err for the integration unless it is the error
// logged last.
func (r *liveRun) integrationLogOnce(id string, err error) {
	i := &r.integ
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.logged[id] == err.Error() {
		return
	}
	if i.logged == nil {
		i.logged = map[string]string{}
	}
	i.logged[id] = err.Error()
	r.log.Warn("integration not readable", "integration", id, "err", err)
}

func (r *liveRun) integrationLogClear(id string) {
	i := &r.integ
	i.mu.Lock()
	delete(i.logged, id)
	i.mu.Unlock()
}

// integrationCheck compares the items of the active profile, which is bound
// to the integration, with the software, and announces differences that are
// new in this run and that the person has not dismissed.
func (r *liveRun) integrationCheck(in integration.Integration, profileID string) {
	lib := r.cfg.opts.library
	if lib == nil {
		return
	}
	id := in.ID()
	key := profileID + "\x00" + id
	items, err := lib.EffectiveIntegration(profileID, id)
	if err != nil {
		r.integrationLogOnce(id, err)
		return
	}
	var statuses []integration.ItemStatus
	if used := r.usedItems(profileID, items); len(used) > 0 {
		if statuses, _, err = in.Compare(used, r.integLang("")); err != nil {
			r.integrationLogOnce(id, err)
			return
		}
	}
	r.integrationLogClear(id)
	diff := integration.DifferenceID(statuses)
	i := &r.integ
	if diff == "" {
		i.mu.Lock()
		delete(i.announced, key)
		i.mu.Unlock()
		return
	}
	if m := r.cfg.opts.memory; m != nil && m.Dismissed(profileID, id) == diff {
		return
	}
	i.mu.Lock()
	if i.announced[key] == diff {
		i.mu.Unlock()
		return
	}
	if i.announced == nil {
		i.announced = map[string]string{}
	}
	i.announced[key] = diff
	i.mu.Unlock()

	count := 0
	for _, s := range statuses {
		if s.State != integration.StateConsistent {
			count++
		}
	}
	r.publishIntegration(rpcIntegrationChanged{Integration: id, Kind: "differences", Profile: profileID, DifferenceID: diff, Count: count})
	r.integrationAnnounce(in, profileID, diff, count)
}

// integrationAnnounce tells the person about differences without taking the
// focus: one toast on the overlay (the topic hud, which only the overlay
// shows) and one tray notification. It never opens or focuses the window.
func (r *liveRun) integrationAnnounce(in integration.Integration, profileID, diff string, count int) {
	name := profileID
	if lib := r.cfg.opts.library; lib != nil {
		if entries, err := lib.List(); err == nil {
			for _, e := range entries {
				if e.ID == profileID && e.Name != "" {
					name = e.Name
				}
			}
		}
	}
	text := integrationNoticeText(r.integLang(""), name, count, in.Name())
	r.log.Info("integration differs", "integration", in.ID(), "profile", profileID, "count", count)
	if r.events.Listening("hud") {
		hudEvents{hub: r.events}.Toast(0, text, 6000)
	}
	n := tray.Notification{Key: "integration:" + profileID + ":" + in.ID() + ":" + diff, Title: "padmate", Body: text}
	switch {
	case r.integ.notify != nil:
		r.integ.notify(n)
	case r.cfg.opts.tray:
		r.notices.Add(n)
	}
}

// integrationNoticeText is the one-line notice: the profile, how many
// shortcuts differ and from which software.
func integrationNoticeText(lang, profileName string, count int, app string) string {
	if lang == "en" {
		return fmt.Sprintf("%s: %d shortcuts differ from %s; open padmate to review", profileName, count, app)
	}
	return fmt.Sprintf("%s：%d 个快捷键和 %s 里的不同，打开 padmate 处理", profileName, count, app)
}
