//go:build !linux

package main

import "errors"

// The full-screen menu is for Linux servers; elsewhere the plain menu is used.
func isTTY(int) bool              { return false }
func makeRaw(int) (func(), error) { return nil, errors.New("no raw terminal") }
func termSize(int) (int, int)     { return 80, 24 }
