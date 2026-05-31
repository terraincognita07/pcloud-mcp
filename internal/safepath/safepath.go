// Package safepath constrains untrusted file and folder names — such as the
// names returned by the pCloud API — to a chosen base directory. It is the
// security boundary of the server: every local path the server writes to MUST
// be built through these functions.
//
// The threat it closes is path traversal via remote metadata. pCloud allows a
// folder or file to be named "..", and an attacker who shares such a structure
// can, in a naive client, walk the download out of its intended directory and
// overwrite arbitrary files (~/.ssh/authorized_keys, ~/.bashrc, cron entries).
// A name may also smuggle a separator that only appears after URL-decoding
// (".."+"%2F"+".."), so validation MUST run on the final, already-decoded name.
package safepath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var (
	// ErrUnsafeName is returned when a single path component is not a plain,
	// separator-free, non-traversal name.
	ErrUnsafeName = errors.New("unsafe path component")
	// ErrEscapes is returned when a joined path would resolve outside the base.
	ErrEscapes = errors.New("path escapes base directory")
)

// reservedWindowsNames are device names the Windows filesystem treats specially
// regardless of extension (CON, CON.txt, ...). They are rejected on every OS so
// that a downloaded tree stays portable and a Windows client is never tricked.
var reservedWindowsNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// SafeName validates that name is a single, benign path component: a real file
// or folder name with no separators, no traversal token, no NUL or control
// bytes, and no Windows-reserved form. On success it returns name unchanged.
//
// It deliberately does not "clean" the input — silently rewriting "../x" to "x"
// would hide an attack. A bad name is an error the caller must surface.
func SafeName(name string) (string, error) {
	switch name {
	case "":
		return "", fmt.Errorf("%w: empty", ErrUnsafeName)
	case ".", "..":
		return "", fmt.Errorf("%w: %q is a traversal token", ErrUnsafeName, name)
	}
	// Any separator (either platform) means this is not a single component.
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%w: %q contains a path separator", ErrUnsafeName, name)
	}
	for _, r := range name {
		switch {
		case r == 0:
			return "", fmt.Errorf("%w: %q contains NUL", ErrUnsafeName, name)
		case r < 0x20:
			return "", fmt.Errorf("%w: %q contains a control character", ErrUnsafeName, name)
		case r == ':':
			// Drive letters ("C:") and NTFS alternate data streams ("x:y").
			return "", fmt.Errorf("%w: %q contains a colon", ErrUnsafeName, name)
		}
	}
	// Windows silently strips trailing dots and spaces, so "evil." can collide
	// with "evil" and "   " can vanish entirely. Forbid those forms outright.
	if strings.TrimRight(name, ". ") != name {
		return "", fmt.Errorf("%w: %q has a trailing dot or space", ErrUnsafeName, name)
	}
	// Reserved device names, case-insensitive, with or without an extension.
	stem := name
	if i := strings.IndexByte(name, '.'); i >= 0 {
		stem = name[:i]
	}
	if _, bad := reservedWindowsNames[strings.ToUpper(stem)]; bad {
		return "", fmt.Errorf("%w: %q is a reserved device name", ErrUnsafeName, name)
	}
	return name, nil
}

// Join validates each component with SafeName, joins them onto base, and then
// verifies — belt and suspenders — that the cleaned result still resolves
// inside base. SafeName alone already makes traversal impossible; the
// containment check is a second, independent gate so the function fails closed
// if either layer is ever weakened.
func Join(base string, parts ...string) (string, error) {
	for _, p := range parts {
		if _, err := SafeName(p); err != nil {
			return "", err
		}
	}
	cleanBase := filepath.Clean(base)
	joined := filepath.Join(append([]string{cleanBase}, parts...)...)
	if err := within(cleanBase, joined); err != nil {
		return "", err
	}
	return joined, nil
}

// within returns ErrEscapes unless target is cleanBase itself or a descendant
// of it, compared lexically.
func within(cleanBase, target string) error {
	rel, err := filepath.Rel(cleanBase, target)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEscapes, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: resolves to %q", ErrEscapes, target)
	}
	return nil
}
