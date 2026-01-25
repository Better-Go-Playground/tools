// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gocommand

import (
	"regexp"
)

// ParseGoVersionOutput extracts the Go version string
// from the output of the "go version" command.
// Given an unrecognized form, it returns an empty string.
func ParseGoVersionOutput(data string) string {
	re := regexp.MustCompile(`^go version (go\S+|devel \S+)`)
	m := re.FindStringSubmatch(data)
	if len(m) != 2 {
		return "" // unrecognized version
	}
	return m[1]
}
