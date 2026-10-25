//go:build !windows

package main

func runNativeTray(r *liveRun) int { return r.wait() }
