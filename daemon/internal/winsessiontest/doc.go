// Package winsessiontest sends window messages to the session window of a
// winsession.Watcher (Watcher.Window), so that tests drive it the way the
// system does: SendMessageW for the session, power and end-of-session
// messages, which the system sends, and PostMessageW for the ones the
// session thread posts itself.
//
// Send and Post refuse, by panicking, any window that is not a
// "padmate.winsession" window of the calling process: a test never sends a
// session, power or end-of-session message to another program, and never
// broadcasts one.
//
// Only tests import this package, which is for Windows only.
package winsessiontest
