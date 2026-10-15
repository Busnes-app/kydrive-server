//go:build race

package drive_test

// raceEnabled marks a -race build, whose 5-20x slowdown makes wall-clock bounds meaningless.
const raceEnabled = true
