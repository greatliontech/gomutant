// Package dot exists so a sibling import path containing a dot is ambiguous
// under anything but longest-match symbol splitting.
package dot

func D() int { return 1 }
